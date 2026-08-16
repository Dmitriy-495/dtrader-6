// Этот файл отвечает ТОЛЬКО за WebSocket-соединение как таковое:
// установить, писать в него потокобезопасно, закрыть.
// Здесь нет ни ping/pong (см. pingloop.go), ни разбора сообщений биржи
// (см. ws.go / будущий parser.go) — только "провод" между нами и Gate.io.
package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/publisher"
	"github.com/gorilla/websocket"
)

// WSClient — WebSocket-клиент Gate.io Futures.
//
// Поля pingTs и emaLat физически хранятся здесь (структура одна на весь
// пакет), но ЛОГИКА, которая их использует (замер латентности, EMA),
// живёт в pingloop.go. В Go так можно: методы одного типа разрешено
// объявлять в разных файлах одного пакета — компилятору всё равно,
// в каком файле лежит код, лишь бы файлы были в одной папке.
type WSClient struct {
	url     string
	apiKey  string
	secret  string
	conn    *websocket.Conn
	writeMu sync.Mutex // защищает conn от одновременной записи из разных горутин
	pub     *publisher.Publisher
	done    chan struct{} // закрывается (НЕ отправляет значение — см. signalDone в ws.go)

	// pingTs — timestamp последнего отправленного ping (unix ms).
	// ПИШЕТСЯ в sendPing (pingloop.go, горутина RunPingLoop), ЧИТАЕТСЯ
	// при получении pong в ReadLoop (ws.go, ДРУГАЯ горутина) — реальная
	// гонка данных без синхронизации, комментарий "используется в
	// pingloop.go" был неточен и вводил в заблуждение (реально читается
	// из ws.go). Найдено независимым аудитом (OpenCode + Claude Sonnet
	// 5, 2026-08-11): на amd64/arm64 разрыва значения не будет
	// (естественно выровненный int64), но без happens-before нет
	// формальной гарантии видимости актуального значения между
	// горутинами, и на 32-битных платформах int64 в принципе не
	// атомарен без sync/atomic. atomic.Int64 устраняет гонку полностью,
	// без цены дополнительного мьютекса на каждую операцию.
	pingTs atomic.Int64
	emaLat float64 // EMA латентности (ms) — пишется и читается только внутри ReadLoop, гонки нет

	// restClient — REST-клиент Gate.io, нужен ТОЛЬКО для одной вещи:
	// получить снапшот стакана (GetOrderBookSnapshot) при инициализации
	// и при пересинхронизации (см. orderbook.go). WSClient и Client (REST)
	// остаются независимыми типами — здесь просто переиспользуется уже
	// существующий REST-клиент, тот же самый, что main.go использует для
	// Ping/GetUnifiedBalance/GetPositions, а не создаётся второй.
	restClient *Client

	// books — локально поддерживаемый полный стакан на каждый символ
	// (см. LocalOrderBook в orderbook.go). booksMu защищает map от
	// одновременного доступа: ReadLoop пишет на каждую входящую дельту,
	// а resyncOrderBook может писать асинхронно из отдельной горутины
	// при обнаружении разрыва последовательности.
	books   map[string]*LocalOrderBook
	booksMu sync.Mutex

	// resyncing отмечает символы, для которых пересинхронизация УЖЕ
	// запущена и ещё не завершилась. Нужно, потому что ReadLoop —
	// последовательный цикл: пока resyncOrderBook ждёт ответ REST (сотни
	// мс), в handleOrderBook продолжают поступать И ОБРАБАТЫВАТЬСЯ новые
	// дельты на СТАРОМ (ещё не обновлённом) c.books[symbol] — каждая из
	// них снова увидит несостыковку lastUpdateID и без этого флага
	// запускала бы ЕЩЁ ОДИН параллельный resyncOrderBook на тот же
	// символ. Несколько одновременных REST-запросов не роняют стакан
	// (какой-то из них в итоге всё равно применится последним), но
	// создают ненужную нагрузку на Gate.io REST и гонку по порядку
	// завершения — не гарантировано, что "победит" именно самый свежий
	// снапшот. Один resync на символ в один момент времени — простое и
	// достаточное решение уровня v1, без очереди/отмены предыдущего
	// запроса (это оверинжиниринг для сценария "иногда рвётся
	// последовательность", а не "рвётся постоянно").
	resyncing map[string]bool

	// generation — счётчик "поколения" WS-соединения, растёт на 1 при
	// каждом ResetDone() (то есть при каждой новой попытке подключения
	// в цикле реконнекта main.go — см. вызов там). Нужен, чтобы
	// resyncOrderBook (fire-and-forget горутина, запущенная через
	// "go c.resyncOrderBook(...)") могла узнать, что WS-сессия, ради
	// которой её запустили, уже мертва — и не писать в c.books
	// устаревший результат поверх свежего стакана следующего поколения.
	//
	// Найдено независимым аудитом (OpenCode + Claude Sonnet 5,
	// 2026-08-11), сценарий: разрыв последовательности на соединении #1
	// → запущен resyncOrderBook (REST-запрос в полёте, до RequestTimeout)
	// → соединение #1 обрывается раньше, чем resync успел завершиться
	// → main.go реконнектится, InitOrderBookSnapshots уже перезаписал
	// c.books свежим снапшотом для НОВОГО потока дельт → горутина-"зомби"
	// от соединения #1 наконец получает ответ REST и БЕЗУСЛОВНО
	// перезаписывает уже актуальный, работающий стакан соединения #2
	// устаревшими данными из другого поколения — откатывая уже
	// применённые к свежему стакану дельты назад, без единого лога об
	// этом конфликте (оба пути логируют "успех").
	generation atomic.Int64
}

// tryStartResync атомарно проверяет и, если для символа ещё не идёт
// пересинхронизация, помечает её начатой — возвращает true, если ИМЕННО
// ЭТОТ вызов получил право запускать resyncOrderBook, false — если для
// символа уже кто-то другой начал resync и его нужно дождаться.
//
// Вынесен в отдельный метод (не инлайн внутри handleOrderBook) по двум
// причинам, обе — по итогам независимого аудита (OpenCode + Claude
// Sonnet 5, 2026-08-10):
//  1. Тестируемость: раньше юнит-тест на защиту от параллельного resync
//     (TestResyncGuard_PreventsParallelResyncForSameSymbol) копировал эту
//     логику в теле теста вместо вызова настоящего продакшн-кода — если
//     бы кто-то сломал guard именно в handleOrderBook, тест продолжил
//     бы проходить, потому что проверял отдельную, не связанную с
//     реальным кодом копию той же логики. Теперь тест вызывает этот
//     метод напрямую — реальный код и тестируемый код гарантированно
//     совпадают.
//  2. Явная документация инварианта: почему вообще нужна эта защита — см.
//     комментарий у поля resyncing выше в этом файле.
func (c *WSClient) tryStartResync(symbol string) bool {
	c.booksMu.Lock()
	defer c.booksMu.Unlock()
	if c.resyncing[symbol] {
		return false
	}
	c.resyncing[symbol] = true
	return true
}

// currentGeneration возвращает номер текущего поколения соединения —
// используется resyncOrderBook, чтобы захватить номер поколения ДО
// начала REST-запроса (см. комментарий у поля generation выше).
func (c *WSClient) currentGeneration() int64 {
	return c.generation.Load()
}

// NewWSClient создаёт новый WS-клиент. Соединение ещё не устанавливается —
// для этого нужно отдельно вызвать Connect.
//
// restClient — REST-клиент Gate.io для получения снапшотов стакана.
// Может быть nil, если orderbook snapshot/resync не нужен (например,
// в будущих unit-тестах, которые проверяют только trades/candles) —
// InitOrderBookSnapshots и resyncOrderBook в этом случае просто не
// сработают (см. проверку c.restClient == nil в orderbook.go), а не
// упадут с паникой.
func NewWSClient(url, apiKey, secret string, pub *publisher.Publisher, restClient *Client) *WSClient {
	return &WSClient{
		url:        url,
		apiKey:     apiKey,
		secret:     secret,
		pub:        pub,
		done:       make(chan struct{}, 1),
		restClient: restClient,
		books:      make(map[string]*LocalOrderBook),
		resyncing:  make(map[string]bool),
	}
}

// Done возвращает канал, который сигналит о разрыве соединения.
// main.go слушает этот канал в select, чтобы понять "пора реконнектиться".
func (c *WSClient) Done() <-chan struct{} {
	return c.done
}

// ResetDone создаёт новый канал done перед каждой новой попыткой подключения
// и увеличивает счётчик поколения (generation) — см. комментарий у поля
// generation выше. Реконнект в main.go начинается с чистого канала И с
// новым номером поколения — обе операции атомарны относительно друг друга
// по построению (обе вызываются последовательно, до Connect, в единственном
// месте — начале итерации цикла реконнекта, никакой другой код их не трогает).
func (c *WSClient) ResetDone() {
	c.done = make(chan struct{}, 1)
	c.generation.Add(1)
}

// writeJSON потокобезопасно пишет JSON-сообщение в соединение.
// Приватный метод — используется sendPing (pingloop.go) и Subscribe*
// методами (subscribe.go).
//
// Зачем мьютекс (writeMu)? WS-соединение читается в одной горутине
// (ReadLoop) и пишется сразу из нескольких: RunPingLoop раз в 10 секунд
// и subscribeAll при старте. Библиотека gorilla/websocket не гарантирует
// потокобезопасность одновременной записи — без мьютекса два Write
// могут "перемешать" байты в сети и сломать протокол.
func (c *WSClient) writeJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}

// writeMessage — низкоуровневая потокобезопасная запись сырых байт.
// Используется только в Close, чтобы отправить корректный close-фрейм
// перед разрывом соединения (это требование протокола WebSocket —
// "вежливое" закрытие, а не обрыв на полуслове).
func (c *WSClient) writeMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

// Connect устанавливает WebSocket-соединение с Gate.io.
// ctx позволяет прервать попытку подключения (например, если пришёл
// SIGTERM прямо во время попытки коннекта).
func (c *WSClient) Connect(ctx context.Context) error {
	header := http.Header{
		// Gate.io присылает размеры (size) как decimal-строки, а не числа,
		// если передать этот заголовок — иначе теряется точность на
		// больших объёмах из-за особенностей JSON-парсинга чисел.
		"X-Gate-Size-Decimal": []string{"1"},
	}
	// Используем явный Dialer с Proxy: nil вместо websocket.DefaultDialer.
	// DefaultDialer по умолчанию тоже читает переменные окружения
	// HTTP_PROXY/HTTPS_PROXY (как и http.Client в client.go) — если
	// в окружении случайно остался мусор от старой настройки прокси,
	// WS-подключение к бирже точно так же встанет колом. Бот должен
	// всегда ходить к Gate.io напрямую, вне зависимости от окружения.
	dialer := &websocket.Dialer{
		Proxy: nil,
	}
	conn, _, err := dialer.DialContext(ctx, c.url, header)
	if err != nil {
		return fmt.Errorf("WS коннект не удался: %w", err)
	}
	c.conn = conn
	log.Printf("✅ WS подключён: %s", c.url)
	return nil
}

// Close корректно закрывает соединение: сначала отправляет close-фрейм
// (это "вежливое прощание" по протоколу WS — сервер узнаёт, что мы
// закрылись сами, а не оборвались из-за сетевой проблемы), потом рвёт
// TCP-соединение целиком.
func (c *WSClient) Close() {
	if c.conn != nil {
		c.writeMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.conn.Close()
	}
}
