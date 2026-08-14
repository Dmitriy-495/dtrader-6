// Этот файл отвечает ТОЛЬКО за поддержание полного, актуального стакана
// в памяти на каждый символ: инициализация из REST-снапшота, применение
// входящих WS-дельт (order_book_update), обнаружение разрывов
// последовательности и пересинхронизация.
//
// Реализует официальный алгоритм Gate.io для локального стакана
// (см. https://www.gate.com/docs/developers/futures/ws/en/#order-book-api,
// раздел "How to maintain local order book"). Документация Gate.io
// описывает это в терминах "U" (начало диапазона дельты) и "u" (конец
// диапазона дельты) — НО в нашем коде (см. OrderBookUpdate в protocol.go)
// поле называется FirstU (= "U" из документации, начало диапазона) и
// поле U (= "u" из документации, конец диапазона, совпадает с полем "u"
// в оригинальном ответе биржи под тем же JSON-ключом "u"). Не путать
// поле u.FirstU с "верхней" u из документации — это разные вещи, несмотря
// на похожие названия.
//
//  1. Подписаться на order_book_update с нужной глубиной/частотой
//  2. Взять REST-снапшот с with_id=true → получить базовый id
//  3. Найти первую дельту, которая "накрывает" этот id:
//     u.FirstU <= id+1 <= u.U
//  4. Применять дельты по цепочке (каждая следующая: u.FirstU == prev.U + 1)
//  5. При разрыве последовательности — заново снапшот + пересинхронизация
//
// Раньше bot публиковал в Redis последнюю ИНКРЕМЕНТАЛЬНУЮ дельту как есть
// (см. CHECKPOINT.md, раздел 13b) — analyzer уже спроектирован под ПОЛНЫЙ
// снапшот по тому же ключу market:orderbook:{symbol}, с этого файла
// начинается доработка, закрывающая это несоответствие.
package gateway

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
)

// bookLevel — один уровень стакана в представлении, удобном для хранения
// и обновления. Ключом карты (map[float64]bookLevel) уже служит цена —
// поэтому сам bookLevel хранит только size (в исходном строковом виде,
// как прислала биржа — не хотим терять точность форматирования decimal-
// значений через промежуточный float64) и priceStr для публикации.
type bookLevel struct {
	sizeStr  string
	priceStr string
}

// LocalOrderBook — поддерживаемый в памяти полный стакан на один символ.
// bids/asks хранятся как map[цена]уровень — обновление/удаление конкретной
// цены O(1), без поиска по срезу. Сортировка по цене происходит только
// при формировании снапшота на публикацию (см. Snapshot ниже).
type LocalOrderBook struct {
	symbol string
	bids   map[float64]bookLevel
	asks   map[float64]bookLevel

	// depth — глубина, с которой был запрошен ПОСЛЕДНИЙ REST-снапшот
	// (параметр limit в GetOrderBookSnapshot), НЕ длина bids/asks на
	// момент создания (биржа теоретически может прислать меньше уровней,
	// чем запрошено, на низколиквидных парах). Источник истины для
	// глубины при будущих resync — она должна оставаться постоянной
	// между вызовами (см. предупреждение в официальной документации
	// Gate.io о необходимости совпадения depth снапшота и level
	// подписки). Раньше (найдено независимым аудитом — OpenCode +
	// Claude Sonnet 5, 2026-08-10) глубина для resync ошибочно бралась
	// из длины ТЕКУЩЕЙ ВХОДЯЩЕЙ ДЕЛЬТЫ в handleOrderBook (parser.go),
	// а не из исходного снапшота — дельта обычно содержит лишь
	// несколько изменившихся уровней, а не полную глубину, из-за чего
	// пересинхронизация могла "урезать" стакан.
	depth int

	// lastUpdateID — последний применённый update ID (поле u из дельты,
	// или id из REST-снапшота, если дельт ещё не было). Следующая
	// валидная дельта должна иметь U == lastUpdateID + 1 — это и есть
	// проверка "разрыва последовательности" из официального алгоритма.
	lastUpdateID int64

	// synced — false сразу после REST-снапшота, пока не встретилась
	// первая дельта, которая корректно "накрывает" базовый id (см.
	// applyFirstDelta). До этого момента дельты просто пропускаются —
	// это ожидаемо, не ошибка (см. комментарий в ApplyDelta).
	synced bool
}

// Depth возвращает глубину, с которой был запрошен исходный REST-снапшот
// этого стакана — используется вызывающим кодом (parser.go) при
// пересинхронизации, чтобы запрашивать ТУ ЖЕ глубину заново, а не
// вычислять её из длины текущей входящей дельты (которая почти всегда
// намного меньше полной глубины).
func (lob *LocalOrderBook) Depth() int {
	return lob.depth
}

// newLocalOrderBook создаёт локальный стакан из REST-снапшота — это
// единственный способ его создать, пустого/нулевого стакана не бывает:
// без базового id дельты нечего накатывать. depth — глубина, с которой
// РЕАЛЬНО был запрошен этот снапшот (параметр limit в самом REST-вызове,
// не len(snap.Bids)/len(snap.Asks) — см. комментарий у поля depth выше).
func newLocalOrderBook(symbol string, snap *OrderBookSnapshot, depth int) *LocalOrderBook {
	lob := &LocalOrderBook{
		symbol:       symbol,
		bids:         make(map[float64]bookLevel, len(snap.Bids)),
		asks:         make(map[float64]bookLevel, len(snap.Asks)),
		depth:        depth,
		lastUpdateID: snap.ID,
		synced:       false,
	}
	for _, lvl := range snap.Bids {
		// lvl.Size — json.Number (REST-формат, см. OBLevelREST в rest.go).
		// .String() отдаёт исходное текстовое представление без потери
		// форматирования — дальше setLevel хранит это же значение как
		// sizeStr, которое публикуется в Redis как есть.
		lob.setLevel(lob.bids, lvl.Price, lvl.Size.String())
	}
	for _, lvl := range snap.Asks {
		lob.setLevel(lob.asks, lvl.Price, lvl.Size.String())
	}
	return lob
}

// setLevel парсит цену из строки и записывает/обновляет уровень в карте.
// Если priceStr не парсится как float — уровень пропускается с логом,
// не паникует и не роняет весь стакан из-за одного кривого значения.
func (lob *LocalOrderBook) setLevel(levels map[float64]bookLevel, priceStr, sizeStr string) {
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		log.Printf("⚠️ orderbook %s: не удалось разобрать цену %q: %v", lob.symbol, priceStr, err)
		return
	}
	levels[price] = bookLevel{sizeStr: sizeStr, priceStr: priceStr}
}

// removeLevel удаляет уровень по цене — вызывается, когда входящая дельта
// присылает size "0" (по протоколу Gate.io это означает "уровень исчез").
func (lob *LocalOrderBook) removeLevel(levels map[float64]bookLevel, priceStr string) {
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		log.Printf("⚠️ orderbook %s: не удалось разобрать цену %q: %v", lob.symbol, priceStr, err)
		return
	}
	delete(levels, price)
}

// ApplyDelta применяет одно входящее WS-сообщение (order_book_update) к
// локальному стакану. Возвращает true, если сообщение было применено
// (стакан обновлён и готов к публикации), false — если оно было
// пропущено (либо ещё не нашли точку стыковки со снапшотом, либо
// обнаружен разрыв последовательности и нужна пересинхронизация,
// см. поле needResync).
func (lob *LocalOrderBook) ApplyDelta(u OrderBookUpdate) (applied bool, needResync bool) {
	if u.Full {
		// Full=true — редкий случай, когда Gate.io присылает через ТОТ ЖЕ
		// канал order_book_update не дельту, а ПОЛНЫЙ снапшот (например,
		// сервер решил, что клиенту нужна принудительная пересинхронизация).
		// По официальной документации: "the local order book should be
		// completely replaced" — не применяем как дельту (не проверяем
		// U/u стыковку), а заменяем bids/asks целиком, как при инициализации
		// из REST.
		//
		// Проверяем монотонность u.U перед заменой: если это устаревшее
		// full-сообщение пришло ПОСЛЕ более новых дельт (переупорядочивание
		// на сети/буферизация), применение отбросило бы уже применённые
		// более свежие обновления назад — молча, без единого сигнала.
		// Устаревший full просто игнорируем: свежее состояние уже лучше,
		// чем то, что несёт с собой этот пакет.
		if u.U <= lob.lastUpdateID {
			log.Printf("⚠️ orderbook %s: устаревший full-снапшот проигнорирован (u.U=%d <= lastUpdateID=%d)",
				lob.symbol, u.U, lob.lastUpdateID)
			return false, false
		}
		lob.bids = make(map[float64]bookLevel, len(u.Bids))
		lob.asks = make(map[float64]bookLevel, len(u.Asks))
		for _, lvl := range u.Bids {
			lob.setLevel(lob.bids, lvl.Price, lvl.Size)
		}
		for _, lvl := range u.Asks {
			lob.setLevel(lob.asks, lvl.Price, lvl.Size)
		}
		lob.lastUpdateID = u.U
		lob.synced = true
		log.Printf("🔄 [orderbook] принудительный full-replace от сервера: %s id=%d bids=%d asks=%d",
			lob.symbol, u.U, len(u.Bids), len(u.Asks))
		return true, false
	}

	if !lob.synced {
		// Ищем точку стыковки со снапшотом: официальный алгоритм Gate.io
		// требует "U <= id+1 <= u" (в терминах документации) — в терминах
		// наших полей это u.FirstU <= lastUpdateID+1 <= u.U. Если эта
		// дельта "новее" точки стыковки (весь её диапазон выше нужного) —
		// нужная дельта, видимо, была раньше и уже потеряна.
		if u.FirstU > lob.lastUpdateID+1 {
			// (не кэшировали историю дельт в этой упрощённой реализации).
			// Идём на пересинхронизацию, а не пытаемся угадать пропущенное.
			return false, true
		}
		if u.U < lob.lastUpdateID+1 {
			// Дельта целиком "младше" точки стыковки — пропускаем и ждём
			// следующую, это ожидаемо на первых нескольких дельтах после
			// свежего REST-снапшота.
			return false, false
		}
		// u.FirstU <= lastUpdateID+1 <= u.U — нашли точку стыковки, начинаем применять.
		lob.synced = true
	} else if u.FirstU != lob.lastUpdateID+1 {
		// Уже синхронизированы, но пришла дельта не следующая по цепочке
		// (её u.FirstU не равен lastUpdateID+1) — разрыв последовательности,
		// часть обновлений потеряна. Официальный алгоритм требует полной
		// пересинхронизации в этом случае — не пытаемся частично залатать дыру.
		return false, true
	}

	for _, lvl := range u.Bids {
		if lvl.Size == "0" {
			lob.removeLevel(lob.bids, lvl.Price)
		} else {
			lob.setLevel(lob.bids, lvl.Price, lvl.Size)
		}
	}
	for _, lvl := range u.Asks {
		if lvl.Size == "0" {
			lob.removeLevel(lob.asks, lvl.Price)
		} else {
			lob.setLevel(lob.asks, lvl.Price, lvl.Size)
		}
	}
	lob.lastUpdateID = u.U
	return true, false
}

// OrderBookFullSnapshot — формат, в котором ПОЛНЫЙ стакан публикуется в
// Redis. Поля названы так же, как в исходном OrderBookUpdate (S/Bids/Asks
// с тегами "s"/"b"/"a", уровни — OBLevel с "p"/"s") — это НЕ случайно:
// analyzer (см. CHECKPOINT.md, раздел 13a) уже спроектирован читать из
// market:orderbook:{symbol} именно эти имена полей, менять их значит
// требовать правок и в analyzer, а весь смысл доработки — обойтись без них.
type OrderBookFullSnapshot struct {
	T    int64     `json:"t"`
	S    string    `json:"s"`
	Bids []OBLevel `json:"b"`
	Asks []OBLevel `json:"a"`
}

// Snapshot формирует срез текущего полного стакана для публикации —
// bids отсортированы по убыванию цены (лучшая покупка сверху), asks по
// возрастанию (лучшая продажа сверху) — так же, как обычно показывают
// стакан в любом торговом интерфейсе, включая будущий TUI.
//
// Цена уже присутствует как ключ карты (map[float64]bookLevel) — сортируем
// по нему напрямую, без повторного strconv.ParseFloat на каждую публикацию
// снапшота (float64-цена парсится один раз, в setLevel, при вставке/
// обновлении уровня — здесь она просто переиспользуется как ключ).
func (lob *LocalOrderBook) Snapshot(tsMs int64) OrderBookFullSnapshot {
	bidPrices := make([]float64, 0, len(lob.bids))
	for price := range lob.bids {
		bidPrices = append(bidPrices, price)
	}
	sort.Slice(bidPrices, func(i, j int) bool {
		return bidPrices[i] > bidPrices[j] // убывание — лучшая (самая высокая) покупка первая
	})
	bids := make([]OBLevel, 0, len(bidPrices))
	for _, price := range bidPrices {
		lvl := lob.bids[price]
		bids = append(bids, OBLevel{Price: lvl.priceStr, Size: lvl.sizeStr})
	}

	askPrices := make([]float64, 0, len(lob.asks))
	for price := range lob.asks {
		askPrices = append(askPrices, price)
	}
	sort.Slice(askPrices, func(i, j int) bool {
		return askPrices[i] < askPrices[j] // возрастание — лучшая (самая низкая) продажа первая
	})
	asks := make([]OBLevel, 0, len(askPrices))
	for _, price := range askPrices {
		lvl := lob.asks[price]
		asks = append(asks, OBLevel{Price: lvl.priceStr, Size: lvl.sizeStr})
	}

	return OrderBookFullSnapshot{T: tsMs, S: lob.symbol, Bids: bids, Asks: asks}
}

// =============================================================================
// Интеграция с WSClient: инициализация снапшотов и пересинхронизация
// =============================================================================

// InitOrderBookSnapshots берёт REST-снапшот для каждого символа и
// инициализирует локальные стаканы. Вызывается один раз при старте,
// ДО подписки на order_book_update (см. main.go) — если подписаться
// раньше снапшота, часть дельт придётся выбросить впустую, ожидая
// точку стыковки, что не страшно функционально, но менее эффективно.
//
// depth — та же глубина, что передаётся в SubscribeOrderBookUpdate —
// официальный алгоритм требует совпадения глубины снапшота и подписки
// (см. предупреждение в документации Gate.io: "The subscribed level
// should match the limit parameter in the REST snapshot").
func (c *WSClient) InitOrderBookSnapshots(ctx context.Context, symbols []string, depth int) error {
	if c.restClient == nil {
		return fmt.Errorf("orderbook snapshot: REST-клиент не задан в WSClient")
	}
	c.booksMu.Lock()
	defer c.booksMu.Unlock()

	for _, symbol := range symbols {
		snap, err := c.restClient.GetOrderBookSnapshot(ctx, symbol, depth)
		if err != nil {
			return fmt.Errorf("orderbook snapshot %s: %w", symbol, err)
		}
		c.books[symbol] = newLocalOrderBook(symbol, snap, depth)
		log.Printf("📖 [orderbook] снапшот получен: %s id=%d bids=%d asks=%d",
			symbol, snap.ID, len(snap.Bids), len(snap.Asks))
	}
	return nil
}

// resyncOrderBook пересинхронизирует стакан ОДНОГО символа — вызывается
// из handleOrderBook (parser.go) при обнаружении разрыва последовательности.
// ctx берётся с коротким таймаутом отдельно от общего жизненного цикла
// соединения — пересинхронизация не должна виснуть дольше, чем на разумный
// REST-запрос, даже если основной ctx ещё долго не отменится.
//
// Вызывающий код (parser.go) обязан выставить c.resyncing[symbol]=true
// ДО запуска этой горутины — здесь только гарантированный сброс флага по
// завершении (через defer), чтобы символ не остался навсегда
// заблокированным для будущих resync, даже если REST-запрос упал с ошибкой.
func (c *WSClient) resyncOrderBook(symbol string, depth int) {
	defer func() {
		c.booksMu.Lock()
		delete(c.resyncing, symbol)
		c.booksMu.Unlock()
	}()

	if c.restClient == nil {
		// В отличие от InitOrderBookSnapshots (которая возвращает явную
		// ошибку при том же условии) — здесь функция вызывается через
		// go c.resyncOrderBook(...) и не возвращает ошибку по дизайну
		// (это fire-and-forget горутина). Раньше эта ветка молчала
		// вообще без лога — при отладке "почему стакан не восстановился
		// после разрыва последовательности" разработчик видел бы только
		// то, что флаг resyncing сброшен, без единого объяснения причины
		// в логах. Найдено независимым аудитом (OpenCode + Claude
		// Sonnet 5, 2026-08-10).
		log.Printf("⚠️ orderbook resync %s пропущен: REST-клиент не задан в WSClient", symbol)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	snap, err := c.restClient.GetOrderBookSnapshot(ctx, symbol, depth)
	if err != nil {
		log.Printf("⚠️ orderbook resync %s failed: %v", symbol, err)
		return
	}
	c.booksMu.Lock()
	c.books[symbol] = newLocalOrderBook(symbol, snap, depth)
	c.booksMu.Unlock()
	log.Printf("🔄 [orderbook] пересинхронизация выполнена: %s id=%d", symbol, snap.ID)
}