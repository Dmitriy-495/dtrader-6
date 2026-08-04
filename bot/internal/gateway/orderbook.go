// Этот файл отвечает ТОЛЬКО за поддержание полного, актуального стакана
// в памяти на каждый символ: инициализация из REST-снапшота, применение
// входящих WS-дельт (order_book_update), обнаружение разрывов
// последовательности и пересинхронизация.
//
// Реализует официальный алгоритм Gate.io для локального стакана
// (см. https://www.gate.com/docs/developers/futures/ws/en/#order-book-api,
// раздел "How to maintain local order book"):
//  1. Подписаться на order_book_update с нужной глубиной/частотой
//  2. Взять REST-снапшот с with_id=true → получить базовый id
//  3. Найти первую дельту, которая "накрывает" этот id (U <= id+1 <= u)
//  4. Применять дельты по цепочке (каждая следующая: U == prev_u + 1)
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
// и обновления: цена как float64 (чтобы сравнивать/сортировать без разбора
// строк на каждой операции) + оригинальная строка размера (публикуем в
// Redis строками, как их присылает биржа — не хотим потерять точность
// форматирования decimal-значений через промежуточный float64).
type bookLevel struct {
	price    float64
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

// newLocalOrderBook создаёт локальный стакан из REST-снапшота — это
// единственный способ его создать, пустого/нулевого стакана не бывает:
// без базового id дельты нечего накатывать.
func newLocalOrderBook(symbol string, snap *OrderBookSnapshot) *LocalOrderBook {
	lob := &LocalOrderBook{
		symbol:       symbol,
		bids:         make(map[float64]bookLevel, len(snap.Bids)),
		asks:         make(map[float64]bookLevel, len(snap.Asks)),
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
	levels[price] = bookLevel{price: price, sizeStr: sizeStr, priceStr: priceStr}
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
		// из REST. Сам факт получения такого сообщения уже равнозначен
		// новой точке стыковки — lastUpdateID берём из u.U.
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
		return true, false
	}

	if !lob.synced {
		// Ищем точку стыковки со снапшотом: официальный алгоритм Gate.io
		// требует U <= lastUpdateID+1 <= u. Если эта дельта "старше" снапшота
		// (её диапазон весь ниже нужной точки) — просто ждём следующую,
		// это НЕ ошибка, а нормальная ситуация сразу после инициализации.
		if u.FirstU > lob.lastUpdateID+1 {
			// Дельта "новее", чем нужная точка стыковки — значит нужная
			// дельта, видимо, была раньше и уже потеряна (не кэшировали
			// историю дельт в этой упрощённой реализации). Идём на
			// пересинхронизацию, а не пытаемся угадать пропущенное.
			return false, true
		}
		if u.U < lob.lastUpdateID+1 {
			// Дельта целиком "младше" точки стыковки — пропускаем и ждём
			// следующую, это ожидаемо на первых нескольких дельтах после
			// свежего REST-снапшота.
			return false, false
		}
		// U <= lastUpdateID+1 <= u — нашли точку стыковки, начинаем применять.
		lob.synced = true
	} else if u.FirstU != lob.lastUpdateID+1 {
		// Уже синхронизированы, но пришла дельта не следующая по цепочке —
		// разрыв последовательности, часть обновлений потеряна.
		// Официальный алгоритм требует полной пересинхронизации в этом
		// случае — не пытаемся частично залатать дыру.
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
func (lob *LocalOrderBook) Snapshot(tsMs int64) OrderBookFullSnapshot {
	bids := make([]OBLevel, 0, len(lob.bids))
	for _, lvl := range lob.bids {
		bids = append(bids, OBLevel{Price: lvl.priceStr, Size: lvl.sizeStr})
	}
	sort.Slice(bids, func(i, j int) bool {
		pi, _ := strconv.ParseFloat(bids[i].Price, 64)
		pj, _ := strconv.ParseFloat(bids[j].Price, 64)
		return pi > pj // убывание — лучшая (самая высокая) покупка первая
	})

	asks := make([]OBLevel, 0, len(lob.asks))
	for _, lvl := range lob.asks {
		asks = append(asks, OBLevel{Price: lvl.priceStr, Size: lvl.sizeStr})
	}
	sort.Slice(asks, func(i, j int) bool {
		pi, _ := strconv.ParseFloat(asks[i].Price, 64)
		pj, _ := strconv.ParseFloat(asks[j].Price, 64)
		return pi < pj // возрастание — лучшая (самая низкая) продажа первая
	})

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
		c.books[symbol] = newLocalOrderBook(symbol, snap)
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
func (c *WSClient) resyncOrderBook(symbol string, depth int) {
	if c.restClient == nil {
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
	c.books[symbol] = newLocalOrderBook(symbol, snap)
	c.booksMu.Unlock()
	log.Printf("🔄 [orderbook] пересинхронизация выполнена: %s id=%d", symbol, snap.ID)
}
