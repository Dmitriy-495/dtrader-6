# Gateway: reconnect и race conditions

**Session ID:** ses_00fc7004cffeyw0bI8TGraZs2d
**Created:** 8/11/2026, 12:48:15 PM
**Updated:** 8/11/2026, 12:52:38 PM

---

## User

Продолжаем сквозной аудит bot/internal/gateway/. Не пиши и не запускай
тестовый код, рассуждай только на основе спецификации Go и логики кода.
Не меняй файлы.

Ответь на 2 вопроса:
1. ЖИЗНЕННЫЙ ЦИКЛ СОЕДИНЕНИЯ: что происходит с состоянием WSClient
   (books, resyncing) при реконнекте? Если reconnect происходит ПОКА
   идёт resyncOrderBook в фоне — что случится с той горутиной?
2. PING/PONG vs ReadLoop: есть ли гонка между ними, защищена ли она?

Для каждой находки — файл+строка, уверенность, влияние. 

---

## Assistant (Build · Claude Sonnet 5 · 6.3s)

_Thinking:_



**Tool: bash**

**Input:**
```json
{
  "command": "find /home/tda/code/dtrader/dtrader-6/bot/internal/gateway -type f -name \"*.go\" | xargs wc -l | tail -1"
}
```

**Output:**
```
  2058 total

```

---

## Assistant (Build · Claude Sonnet 5 · 2.7s)

**Tool: bash**

**Input:**
```json
{
  "command": "find /home/tda/code/dtrader/dtrader-6/bot/internal/gateway -type f -name \"*.go\" | sort"
}
```

**Output:**
```
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/client.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/orderbook.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/orderbook_test.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/parser.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/parser_test.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/protocol.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/rest.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/subscribe.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/ws.go

```

---

## Assistant (Build · Claude Sonnet 5 · 4.0s)

**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/client.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/client.go</path>
<type>file</type>
<content>
1: // Пакет gateway отвечает за всё взаимодействие с биржей Gate.io.
2: // Этот файл реализует базовый HTTP клиент для REST API запросов.
3: //
4: // Архитектура пакета:
5: //
6: //	constants.go — константы пакета
7: //	client.go    — низкоуровневый HTTP клиент (этот файл)
8: //	rest.go      — высокоуровневые методы: Ping, Balance, Positions
9: package gateway
10: 
11: import (
12: 	"bytes"
13: 	"context"
14: 	"encoding/json"
15: 	"fmt"
16: 	"io"
17: 	"net/http"
18: 
19: 	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
20: )
21: 
22: // Client — HTTP клиент для REST API Gate.io.
23: // Создаётся один раз при старте и переиспользуется для всех запросов.
24: type Client struct {
25: 	// apiKey — приватное поле, недоступно вне пакета gateway.
26: 	apiKey string
27: 	// secret — приватное поле, аналогично apiKey.
28: 	secret string
29: 	// baseURL — базовый URL REST API, например "https://api.gateio.ws/api/v4".
30: 	baseURL string
31: 	// httpClient — стандартный HTTP клиент Go с таймаутом.
32: 	// Указатель *http.Client — содержит мьютексы, копировать нельзя.
33: 	httpClient *http.Client
34: }
35: 
36: // NewClient создаёт новый Client для работы с Gate.io REST API.
37: // Конструктор — стандартный паттерн Go: New+ИмяТипа.
38: func NewClient(apiKey, secret, baseURL string) *Client {
39: 	return &Client{
40: 		apiKey:  apiKey,
41: 		secret:  secret,
42: 		baseURL: baseURL,
43: 		// Таймаут из константы — не магическое число!
44: 		httpClient: &http.Client{
45: 			Timeout: requestTimeout,
46: 		},
47: 	}
48: }
49: 
50: // buildURL формирует полный URL из baseURL + endpoint + query.
51: // Приватный метод — используется только внутри пакета gateway.
52: //
53: // Примеры:
54: //
55: //	buildURL("/futures/usdt/accounts", "")         → "https://.../futures/usdt/accounts"
56: //	buildURL("/futures/usdt/contracts", "limit=1") → "https://.../futures/usdt/contracts?limit=1"
57: func (c *Client) buildURL(endpoint, query string) string {
58: 	url := c.baseURL + endpoint
59: 	if query != "" {
60: 		url = url + "?" + query
61: 	}
62: 	return url
63: }
64: 
65: // setCommonHeaders устанавливает стандартные заголовки для всех запросов.
66: // Приватный метод — убирает дублирование между Get, GetPublic, Post.
67: // Content-Type, Accept и User-Agent нужны всем запросам без исключения.
68: func (c *Client) setCommonHeaders(req *http.Request) {
69: 	req.Header.Set("Content-Type", contentType)
70: 	req.Header.Set("Accept", contentType)
71: 	req.Header.Set("User-Agent", userAgent)
72: }
73: 
74: // readResponse проверяет статус HTTP ответа и десериализует JSON в result.
75: // Приватный метод — убирает дублирование из Get, GetPublic, Post.
76: func (c *Client) readResponse(resp *http.Response, endpoint string, result interface{}) error {
77: 	// Gate.io возвращает 200 для GET и 200/201 для POST.
78: 	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
79: 		// Читаем тело ответа для понятного сообщения об ошибке.
80: 		body, _ := io.ReadAll(resp.Body)
81: 		return fmt.Errorf("Gate.io вернул статус %d для %s: %s",
82: 			resp.StatusCode, endpoint, string(body))
83: 	}
84: 
85: 	// Декодируем JSON прямо из тела ответа без промежуточного буфера.
86: 	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
87: 		return fmt.Errorf("ошибка десериализации ответа %s: %w", endpoint, err)
88: 	}
89: 
90: 	return nil
91: }
92: 
93: // Get выполняет авторизованный GET запрос к Gate.io REST API.
94: //
95: // Параметры:
96: //   - ctx      : контекст — позволяет отменить запрос по таймауту
97: //   - endpoint : путь без baseURL, например "/futures/usdt/accounts"
98: //   - query    : параметры запроса, например "limit=10" или ""
99: //   - result   : указатель на структуру для десериализации ответа
100: func (c *Client) Get(ctx context.Context, endpoint, query string, result interface{}) error {
101: 	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
102: 		c.buildURL(endpoint, query), nil)
103: 	if err != nil {
104: 		return fmt.Errorf("ошибка создания GET запроса %s: %w", endpoint, err)
105: 	}
106: 
107: 	// Сначала общие заголовки, потом авторизация.
108: 	// AuthHeaders перезапишет Content-Type если нужно — это нормально.
109: 	c.setCommonHeaders(req)
110: 	utils.AuthHeaders(req, c.apiKey, c.secret, "")
111: 
112: 	resp, err := c.httpClient.Do(req)
113: 	if err != nil {
114: 		return fmt.Errorf("ошибка выполнения GET запроса %s: %w", endpoint, err)
115: 	}
116: 	defer resp.Body.Close()
117: 
118: 	return c.readResponse(resp, endpoint, result)
119: }
120: 
121: // GetPublic выполняет НЕ авторизованный GET запрос к Gate.io REST API.
122: // Используется для публичных endpoints: ping, список контрактов и т.д.
123: func (c *Client) GetPublic(ctx context.Context, endpoint, query string, result interface{}) error {
124: 	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
125: 		c.buildURL(endpoint, query), nil)
126: 	if err != nil {
127: 		return fmt.Errorf("ошибка создания публичного GET запроса %s: %w", endpoint, err)
128: 	}
129: 
130: 	// Только общие заголовки — без подписи!
131: 	c.setCommonHeaders(req)
132: 
133: 	resp, err := c.httpClient.Do(req)
134: 	if err != nil {
135: 		return fmt.Errorf("ошибка выполнения публичного GET запроса %s: %w", endpoint, err)
136: 	}
137: 	defer resp.Body.Close()
138: 
139: 	return c.readResponse(resp, endpoint, result)
140: }
141: 
142: // Post выполняет авторизованный POST запрос к Gate.io REST API.
143: // Используется для создания ордеров и других операций записи.
144: // Фундамент для trader сервиса — пока не используется активно.
145: func (c *Client) Post(ctx context.Context, endpoint string, payload, result interface{}) error {
146: 	// Сериализуем payload в JSON байты.
147: 	bodyBytes, err := json.Marshal(payload)
148: 	if err != nil {
149: 		return fmt.Errorf("ошибка сериализации тела запроса %s: %w", endpoint, err)
150: 	}
151: 
152: 	// bytes.NewReader создаёт io.Reader из байтового среза.
153: 	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
154: 		c.buildURL(endpoint, ""), bytes.NewReader(bodyBytes))
155: 	if err != nil {
156: 		return fmt.Errorf("ошибка создания POST запроса %s: %w", endpoint, err)
157: 	}
158: 
159: 	// Общие заголовки + авторизация с телом запроса.
160: 	// Gate.io проверяет что тело не было изменено после подписания!
161: 	c.setCommonHeaders(req)
162: 	utils.AuthHeaders(req, c.apiKey, c.secret, string(bodyBytes))
163: 
164: 	resp, err := c.httpClient.Do(req)
165: 	if err != nil {
166: 		return fmt.Errorf("ошибка выполнения POST запроса %s: %w", endpoint, err)
167: 	}
168: 	defer resp.Body.Close()
169: 
170: 	return c.readResponse(resp, endpoint, result)
171: }

(End of file - total 171 lines)
</content>
```

**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go</path>
<type>file</type>
<content>
1: // Этот файл отвечает ТОЛЬКО за WebSocket-соединение как таковое:
2: // установить, писать в него потокобезопасно, закрыть.
3: // Здесь нет ни ping/pong (см. pingloop.go), ни разбора сообщений биржи
4: // (см. ws.go / будущий parser.go) — только "провод" между нами и Gate.io.
5: package gateway
6: 
7: import (
8: 	"context"
9: 	"fmt"
10: 	"log"
11: 	"net/http"
12: 	"sync"
13: 
14: 	"github.com/Dmitriy-495/dtrader-6/bot/internal/publisher"
15: 	"github.com/gorilla/websocket"
16: )
17: 
18: // WSClient — WebSocket-клиент Gate.io Futures.
19: //
20: // Поля pingTs и emaLat физически хранятся здесь (структура одна на весь
21: // пакет), но ЛОГИКА, которая их использует (замер латентности, EMA),
22: // живёт в pingloop.go. В Go так можно: методы одного типа разрешено
23: // объявлять в разных файлах одного пакета — компилятору всё равно,
24: // в каком файле лежит код, лишь бы файлы были в одной папке.
25: type WSClient struct {
26: 	url     string
27: 	apiKey  string
28: 	secret  string
29: 	conn    *websocket.Conn
30: 	writeMu sync.Mutex // защищает conn от одновременной записи из разных горутин
31: 	pub     *publisher.Publisher
32: 	done    chan struct{} // закрывается/сигналит, когда соединение разорвано
33: 
34: 	pingTs int64   // timestamp последнего отправленного ping (unix ms) — используется в pingloop.go
35: 	emaLat float64 // EMA латентности (ms) — используется в pingloop.go
36: 
37: 	// restClient — REST-клиент Gate.io, нужен ТОЛЬКО для одной вещи:
38: 	// получить снапшот стакана (GetOrderBookSnapshot) при инициализации
39: 	// и при пересинхронизации (см. orderbook.go). WSClient и Client (REST)
40: 	// остаются независимыми типами — здесь просто переиспользуется уже
41: 	// существующий REST-клиент, тот же самый, что main.go использует для
42: 	// Ping/GetUnifiedBalance/GetPositions, а не создаётся второй.
43: 	restClient *Client
44: 
45: 	// books — локально поддерживаемый полный стакан на каждый символ
46: 	// (см. LocalOrderBook в orderbook.go). booksMu защищает map от
47: 	// одновременного доступа: ReadLoop пишет на каждую входящую дельту,
48: 	// а resyncOrderBook может писать асинхронно из отдельной горутины
49: 	// при обнаружении разрыва последовательности.
50: 	books   map[string]*LocalOrderBook
51: 	booksMu sync.Mutex
52: 
53: 	// resyncing отмечает символы, для которых пересинхронизация УЖЕ
54: 	// запущена и ещё не завершилась. Нужно, потому что ReadLoop —
55: 	// последовательный цикл: пока resyncOrderBook ждёт ответ REST (сотни
56: 	// мс), в handleOrderBook продолжают поступать И ОБРАБАТЫВАТЬСЯ новые
57: 	// дельты на СТАРОМ (ещё не обновлённом) c.books[symbol] — каждая из
58: 	// них снова увидит несостыковку lastUpdateID и без этого флага
59: 	// запускала бы ЕЩЁ ОДИН параллельный resyncOrderBook на тот же
60: 	// символ. Несколько одновременных REST-запросов не роняют стакан
61: 	// (какой-то из них в итоге всё равно применится последним), но
62: 	// создают ненужную нагрузку на Gate.io REST и гонку по порядку
63: 	// завершения — не гарантировано, что "победит" именно самый свежий
64: 	// снапшот. Один resync на символ в один момент времени — простое и
65: 	// достаточное решение уровня v1, без очереди/отмены предыдущего
66: 	// запроса (это оверинжиниринг для сценария "иногда рвётся
67: 	// последовательность", а не "рвётся постоянно").
68: 	resyncing map[string]bool
69: }
70: 
71: // tryStartResync атомарно проверяет и, если для символа ещё не идёт
72: // пересинхронизация, помечает её начатой — возвращает true, если ИМЕННО
73: // ЭТОТ вызов получил право запускать resyncOrderBook, false — если для
74: // символа уже кто-то другой начал resync и его нужно дождаться.
75: //
76: // Вынесен в отдельный метод (не инлайн внутри handleOrderBook) по двум
77: // причинам, обе — по итогам независимого аудита (OpenCode + Claude
78: // Sonnet 5, 2026-08-10):
79: //  1. Тестируемость: раньше юнит-тест на защиту от параллельного resync
80: //     (TestResyncGuard_PreventsParallelResyncForSameSymbol) копировал эту
81: //     логику в теле теста вместо вызова настоящего продакшн-кода — если
82: //     бы кто-то сломал guard именно в handleOrderBook, тест продолжил
83: //     бы проходить, потому что проверял отдельную, не связанную с
84: //     реальным кодом копию той же логики. Теперь тест вызывает этот
85: //     метод напрямую — реальный код и тестируемый код гарантированно
86: //     совпадают.
87: //  2. Явная документация инварианта: почему вообще нужна эта защита — см.
88: //     комментарий у поля resyncing выше в этом файле.
89: func (c *WSClient) tryStartResync(symbol string) bool {
90: 	c.booksMu.Lock()
91: 	defer c.booksMu.Unlock()
92: 	if c.resyncing[symbol] {
93: 		return false
94: 	}
95: 	c.resyncing[symbol] = true
96: 	return true
97: }
98: 
99: // NewWSClient создаёт новый WS-клиент. Соединение ещё не устанавливается —
100: // для этого нужно отдельно вызвать Connect.
101: //
102: // restClient — REST-клиент Gate.io для получения снапшотов стакана.
103: // Может быть nil, если orderbook snapshot/resync не нужен (например,
104: // в будущих unit-тестах, которые проверяют только trades/candles) —
105: // InitOrderBookSnapshots и resyncOrderBook в этом случае просто не
106: // сработают (см. проверку c.restClient == nil в orderbook.go), а не
107: // упадут с паникой.
108: func NewWSClient(url, apiKey, secret string, pub *publisher.Publisher, restClient *Client) *WSClient {
109: 	return &WSClient{
110: 		url:        url,
111: 		apiKey:     apiKey,
112: 		secret:     secret,
113: 		pub:        pub,
114: 		done:       make(chan struct{}, 1),
115: 		restClient: restClient,
116: 		books:      make(map[string]*LocalOrderBook),
117: 		resyncing:  make(map[string]bool),
118: 	}
119: }
120: 
121: // Done возвращает канал, который сигналит о разрыве соединения.
122: // main.go слушает этот канал в select, чтобы понять "пора реконнектиться".
123: func (c *WSClient) Done() <-chan struct{} {
124: 	return c.done
125: }
126: 
127: // ResetDone создаёт новый канал done перед каждой новой попыткой подключения.
128: // Нужно, потому что закрытый (или уже просигналивший) канал нельзя
129: // переиспользовать — реконнект в main.go начинается с чистого канала.
130: func (c *WSClient) ResetDone() {
131: 	c.done = make(chan struct{}, 1)
132: }
133: 
134: // writeJSON потокобезопасно пишет JSON-сообщение в соединение.
135: // Приватный метод — используется sendPing (pingloop.go) и Subscribe*
136: // методами (subscribe.go).
137: //
138: // Зачем мьютекс (writeMu)? WS-соединение читается в одной горутине
139: // (ReadLoop) и пишется сразу из нескольких: RunPingLoop раз в 10 секунд
140: // и subscribeAll при старте. Библиотека gorilla/websocket не гарантирует
141: // потокобезопасность одновременной записи — без мьютекса два Write
142: // могут "перемешать" байты в сети и сломать протокол.
143: func (c *WSClient) writeJSON(v interface{}) error {
144: 	c.writeMu.Lock()
145: 	defer c.writeMu.Unlock()
146: 	return c.conn.WriteJSON(v)
147: }
148: 
149: // writeMessage — низкоуровневая потокобезопасная запись сырых байт.
150: // Используется только в Close, чтобы отправить корректный close-фрейм
151: // перед разрывом соединения (это требование протокола WebSocket —
152: // "вежливое" закрытие, а не обрыв на полуслове).
153: func (c *WSClient) writeMessage(messageType int, data []byte) error {
154: 	c.writeMu.Lock()
155: 	defer c.writeMu.Unlock()
156: 	return c.conn.WriteMessage(messageType, data)
157: }
158: 
159: // Connect устанавливает WebSocket-соединение с Gate.io.
160: // ctx позволяет прервать попытку подключения (например, если пришёл
161: // SIGTERM прямо во время попытки коннекта).
162: func (c *WSClient) Connect(ctx context.Context) error {
163: 	header := http.Header{
164: 		// Gate.io присылает размеры (size) как decimal-строки, а не числа,
165: 		// если передать этот заголовок — иначе теряется точность на
166: 		// больших объёмах из-за особенностей JSON-парсинга чисел.
167: 		"X-Gate-Size-Decimal": []string{"1"},
168: 	}
169: 	// Используем явный Dialer с Proxy: nil вместо websocket.DefaultDialer.
170: 	// DefaultDialer по умолчанию тоже читает переменные окружения
171: 	// HTTP_PROXY/HTTPS_PROXY (как и http.Client в client.go) — если
172: 	// в окружении случайно остался мусор от старой настройки прокси,
173: 	// WS-подключение к бирже точно так же встанет колом. Бот должен
174: 	// всегда ходить к Gate.io напрямую, вне зависимости от окружения.
175: 	dialer := &websocket.Dialer{
176: 		Proxy: nil,
177: 	}
178: 	conn, _, err := dialer.DialContext(ctx, c.url, header)
179: 	if err != nil {
180: 		return fmt.Errorf("WS коннект не удался: %w", err)
181: 	}
182: 	c.conn = conn
183: 	log.Printf("✅ WS подключён: %s", c.url)
184: 	return nil
185: }
186: 
187: // Close корректно закрывает соединение: сначала отправляет close-фрейм
188: // (это "вежливое прощание" по протоколу WS — сервер узнаёт, что мы
189: // закрылись сами, а не оборвались из-за сетевой проблемы), потом рвёт
190: // TCP-соединение целиком.
191: func (c *WSClient) Close() {
192: 	if c.conn != nil {
193: 		c.writeMessage(
194: 			websocket.CloseMessage,
195: 			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
196: 		)
197: 		c.conn.Close()
198: 	}
199: }

(End of file - total 199 lines)
</content>
```

---

## Assistant (Build · Claude Sonnet 5 · 4.9s)

_Thinking:_



**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/orderbook.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/orderbook.go</path>
<type>file</type>
<content>
1: // Этот файл отвечает ТОЛЬКО за поддержание полного, актуального стакана
2: // в памяти на каждый символ: инициализация из REST-снапшота, применение
3: // входящих WS-дельт (order_book_update), обнаружение разрывов
4: // последовательности и пересинхронизация.
5: //
6: // Реализует официальный алгоритм Gate.io для локального стакана
7: // (см. https://www.gate.com/docs/developers/futures/ws/en/#order-book-api,
8: // раздел "How to maintain local order book"). Документация Gate.io
9: // описывает это в терминах "U" (начало диапазона дельты) и "u" (конец
10: // диапазона дельты) — НО в нашем коде (см. OrderBookUpdate в protocol.go)
11: // поле называется FirstU (= "U" из документации, начало диапазона) и
12: // поле U (= "u" из документации, конец диапазона, совпадает с полем "u"
13: // в оригинальном ответе биржи под тем же JSON-ключом "u"). Не путать
14: // поле u.FirstU с "верхней" u из документации — это разные вещи, несмотря
15: // на похожие названия.
16: //
17: //  1. Подписаться на order_book_update с нужной глубиной/частотой
18: //  2. Взять REST-снапшот с with_id=true → получить базовый id
19: //  3. Найти первую дельту, которая "накрывает" этот id:
20: //     u.FirstU <= id+1 <= u.U
21: //  4. Применять дельты по цепочке (каждая следующая: u.FirstU == prev.U + 1)
22: //  5. При разрыве последовательности — заново снапшот + пересинхронизация
23: //
24: // Раньше bot публиковал в Redis последнюю ИНКРЕМЕНТАЛЬНУЮ дельту как есть
25: // (см. CHECKPOINT.md, раздел 13b) — analyzer уже спроектирован под ПОЛНЫЙ
26: // снапшот по тому же ключу market:orderbook:{symbol}, с этого файла
27: // начинается доработка, закрывающая это несоответствие.
28: package gateway
29: 
30: import (
31: 	"context"
32: 	"fmt"
33: 	"log"
34: 	"sort"
35: 	"strconv"
36: )
37: 
38: // bookLevel — один уровень стакана в представлении, удобном для хранения
39: // и обновления. Ключом карты (map[float64]bookLevel) уже служит цена —
40: // поэтому сам bookLevel хранит только size (в исходном строковом виде,
41: // как прислала биржа — не хотим терять точность форматирования decimal-
42: // значений через промежуточный float64) и priceStr для публикации.
43: type bookLevel struct {
44: 	sizeStr  string
45: 	priceStr string
46: }
47: 
48: // LocalOrderBook — поддерживаемый в памяти полный стакан на один символ.
49: // bids/asks хранятся как map[цена]уровень — обновление/удаление конкретной
50: // цены O(1), без поиска по срезу. Сортировка по цене происходит только
51: // при формировании снапшота на публикацию (см. Snapshot ниже).
52: type LocalOrderBook struct {
53: 	symbol string
54: 	bids   map[float64]bookLevel
55: 	asks   map[float64]bookLevel
56: 
57: 	// depth — глубина, с которой был запрошен ПОСЛЕДНИЙ REST-снапшот
58: 	// (параметр limit в GetOrderBookSnapshot), НЕ длина bids/asks на
59: 	// момент создания (биржа теоретически может прислать меньше уровней,
60: 	// чем запрошено, на низколиквидных парах). Источник истины для
61: 	// глубины при будущих resync — она должна оставаться постоянной
62: 	// между вызовами (см. предупреждение в официальной документации
63: 	// Gate.io о необходимости совпадения depth снапшота и level
64: 	// подписки). Раньше (найдено независимым аудитом — OpenCode +
65: 	// Claude Sonnet 5, 2026-08-10) глубина для resync ошибочно бралась
66: 	// из длины ТЕКУЩЕЙ ВХОДЯЩЕЙ ДЕЛЬТЫ в handleOrderBook (parser.go),
67: 	// а не из исходного снапшота — дельта обычно содержит лишь
68: 	// несколько изменившихся уровней, а не полную глубину, из-за чего
69: 	// пересинхронизация могла "урезать" стакан.
70: 	depth int
71: 
72: 	// lastUpdateID — последний применённый update ID (поле u из дельты,
73: 	// или id из REST-снапшота, если дельт ещё не было). Следующая
74: 	// валидная дельта должна иметь U == lastUpdateID + 1 — это и есть
75: 	// проверка "разрыва последовательности" из официального алгоритма.
76: 	lastUpdateID int64
77: 
78: 	// synced — false сразу после REST-снапшота, пока не встретилась
79: 	// первая дельта, которая корректно "накрывает" базовый id (см.
80: 	// applyFirstDelta). До этого момента дельты просто пропускаются —
81: 	// это ожидаемо, не ошибка (см. комментарий в ApplyDelta).
82: 	synced bool
83: }
84: 
85: // Depth возвращает глубину, с которой был запрошен исходный REST-снапшот
86: // этого стакана — используется вызывающим кодом (parser.go) при
87: // пересинхронизации, чтобы запрашивать ТУ ЖЕ глубину заново, а не
88: // вычислять её из длины текущей входящей дельты (которая почти всегда
89: // намного меньше полной глубины).
90: func (lob *LocalOrderBook) Depth() int {
91: 	return lob.depth
92: }
93: 
94: // newLocalOrderBook создаёт локальный стакан из REST-снапшота — это
95: // единственный способ его создать, пустого/нулевого стакана не бывает:
96: // без базового id дельты нечего накатывать. depth — глубина, с которой
97: // РЕАЛЬНО был запрошен этот снапшот (параметр limit в самом REST-вызове,
98: // не len(snap.Bids)/len(snap.Asks) — см. комментарий у поля depth выше).
99: func newLocalOrderBook(symbol string, snap *OrderBookSnapshot, depth int) *LocalOrderBook {
100: 	lob := &LocalOrderBook{
101: 		symbol:       symbol,
102: 		bids:         make(map[float64]bookLevel, len(snap.Bids)),
103: 		asks:         make(map[float64]bookLevel, len(snap.Asks)),
104: 		depth:        depth,
105: 		lastUpdateID: snap.ID,
106: 		synced:       false,
107: 	}
108: 	for _, lvl := range snap.Bids {
109: 		// lvl.Size — json.Number (REST-формат, см. OBLevelREST в rest.go).
110: 		// .String() отдаёт исходное текстовое представление без потери
111: 		// форматирования — дальше setLevel хранит это же значение как
112: 		// sizeStr, которое публикуется в Redis как есть.
113: 		lob.setLevel(lob.bids, lvl.Price, lvl.Size.String())
114: 	}
115: 	for _, lvl := range snap.Asks {
116: 		lob.setLevel(lob.asks, lvl.Price, lvl.Size.String())
117: 	}
118: 	return lob
119: }
120: 
121: // setLevel парсит цену из строки и записывает/обновляет уровень в карте.
122: // Если priceStr не парсится как float — уровень пропускается с логом,
123: // не паникует и не роняет весь стакан из-за одного кривого значения.
124: func (lob *LocalOrderBook) setLevel(levels map[float64]bookLevel, priceStr, sizeStr string) {
125: 	price, err := strconv.ParseFloat(priceStr, 64)
126: 	if err != nil {
127: 		log.Printf("⚠️ orderbook %s: не удалось разобрать цену %q: %v", lob.symbol, priceStr, err)
128: 		return
129: 	}
130: 	levels[price] = bookLevel{sizeStr: sizeStr, priceStr: priceStr}
131: }
132: 
133: // removeLevel удаляет уровень по цене — вызывается, когда входящая дельта
134: // присылает size "0" (по протоколу Gate.io это означает "уровень исчез").
135: func (lob *LocalOrderBook) removeLevel(levels map[float64]bookLevel, priceStr string) {
136: 	price, err := strconv.ParseFloat(priceStr, 64)
137: 	if err != nil {
138: 		log.Printf("⚠️ orderbook %s: не удалось разобрать цену %q: %v", lob.symbol, priceStr, err)
139: 		return
140: 	}
141: 	delete(levels, price)
142: }
143: 
144: // ApplyDelta применяет одно входящее WS-сообщение (order_book_update) к
145: // локальному стакану. Возвращает true, если сообщение было применено
146: // (стакан обновлён и готов к публикации), false — если оно было
147: // пропущено (либо ещё не нашли точку стыковки со снапшотом, либо
148: // обнаружен разрыв последовательности и нужна пересинхронизация,
149: // см. поле needResync).
150: func (lob *LocalOrderBook) ApplyDelta(u OrderBookUpdate) (applied bool, needResync bool) {
151: 	if u.Full {
152: 		// Full=true — редкий случай, когда Gate.io присылает через ТОТ ЖЕ
153: 		// канал order_book_update не дельту, а ПОЛНЫЙ снапшот (например,
154: 		// сервер решил, что клиенту нужна принудительная пересинхронизация).
155: 		// По официальной документации: "the local order book should be
156: 		// completely replaced" — не применяем как дельту (не проверяем
157: 		// U/u стыковку), а заменяем bids/asks целиком, как при инициализации
158: 		// из REST.
159: 		//
160: 		// Проверяем монотонность u.U перед заменой: если это устаревшее
161: 		// full-сообщение пришло ПОСЛЕ более новых дельт (переупорядочивание
162: 		// на сети/буферизация), применение отбросило бы уже применённые
163: 		// более свежие обновления назад — молча, без единого сигнала.
164: 		// Устаревший full просто игнорируем: свежее состояние уже лучше,
165: 		// чем то, что несёт с собой этот пакет.
166: 		if u.U <= lob.lastUpdateID {
167: 			log.Printf("⚠️ orderbook %s: устаревший full-снапшот проигнорирован (u.U=%d <= lastUpdateID=%d)",
168: 				lob.symbol, u.U, lob.lastUpdateID)
169: 			return false, false
170: 		}
171: 		lob.bids = make(map[float64]bookLevel, len(u.Bids))
172: 		lob.asks = make(map[float64]bookLevel, len(u.Asks))
173: 		for _, lvl := range u.Bids {
174: 			lob.setLevel(lob.bids, lvl.Price, lvl.Size)
175: 		}
176: 		for _, lvl := range u.Asks {
177: 			lob.setLevel(lob.asks, lvl.Price, lvl.Size)
178: 		}
179: 		lob.lastUpdateID = u.U
180: 		lob.synced = true
181: 		log.Printf("🔄 [orderbook] принудительный full-replace от сервера: %s id=%d bids=%d asks=%d",
182: 			lob.symbol, u.U, len(u.Bids), len(u.Asks))
183: 		return true, false
184: 	}
185: 
186: 	if !lob.synced {
187: 		// Ищем точку стыковки со снапшотом: официальный алгоритм Gate.io
188: 		// требует "U <= id+1 <= u" (в терминах документации) — в терминах
189: 		// наших полей это u.FirstU <= lastUpdateID+1 <= u.U. Если эта
190: 		// дельта "новее" точки стыковки (весь её диапазон выше нужного) —
191: 		// нужная дельта, видимо, была раньше и уже потеряна.
192: 		if u.FirstU > lob.lastUpdateID+1 {
193: 			// (не кэшировали историю дельт в этой упрощённой реализации).
194: 			// Идём на пересинхронизацию, а не пытаемся угадать пропущенное.
195: 			return false, true
196: 		}
197: 		if u.U < lob.lastUpdateID+1 {
198: 			// Дельта целиком "младше" точки стыковки — пропускаем и ждём
199: 			// следующую, это ожидаемо на первых нескольких дельтах после
200: 			// свежего REST-снапшота.
201: 			return false, false
202: 		}
203: 		// u.FirstU <= lastUpdateID+1 <= u.U — нашли точку стыковки, начинаем применять.
204: 		lob.synced = true
205: 	} else if u.FirstU != lob.lastUpdateID+1 {
206: 		// Уже синхронизированы, но пришла дельта не следующая по цепочке
207: 		// (её u.FirstU не равен lastUpdateID+1) — разрыв последовательности,
208: 		// часть обновлений потеряна. Официальный алгоритм требует полной
209: 		// пересинхронизации в этом случае — не пытаемся частично залатать дыру.
210: 		return false, true
211: 	}
212: 
213: 	for _, lvl := range u.Bids {
214: 		if lvl.Size == "0" {
215: 			lob.removeLevel(lob.bids, lvl.Price)
216: 		} else {
217: 			lob.setLevel(lob.bids, lvl.Price, lvl.Size)
218: 		}
219: 	}
220: 	for _, lvl := range u.Asks {
221: 		if lvl.Size == "0" {
222: 			lob.removeLevel(lob.asks, lvl.Price)
223: 		} else {
224: 			lob.setLevel(lob.asks, lvl.Price, lvl.Size)
225: 		}
226: 	}
227: 	lob.lastUpdateID = u.U
228: 	return true, false
229: }
230: 
231: // OrderBookFullSnapshot — формат, в котором ПОЛНЫЙ стакан публикуется в
232: // Redis. Поля названы так же, как в исходном OrderBookUpdate (S/Bids/Asks
233: // с тегами "s"/"b"/"a", уровни — OBLevel с "p"/"s") — это НЕ случайно:
234: // analyzer (см. CHECKPOINT.md, раздел 13a) уже спроектирован читать из
235: // market:orderbook:{symbol} именно эти имена полей, менять их значит
236: // требовать правок и в analyzer, а весь смысл доработки — обойтись без них.
237: type OrderBookFullSnapshot struct {
238: 	T    int64     `json:"t"`
239: 	S    string    `json:"s"`
240: 	Bids []OBLevel `json:"b"`
241: 	Asks []OBLevel `json:"a"`
242: }
243: 
244: // Snapshot формирует срез текущего полного стакана для публикации —
245: // bids отсортированы по убыванию цены (лучшая покупка сверху), asks по
246: // возрастанию (лучшая продажа сверху) — так же, как обычно показывают
247: // стакан в любом торговом интерфейсе, включая будущий TUI.
248: //
249: // Цена уже присутствует как ключ карты (map[float64]bookLevel) — сортируем
250: // по нему напрямую, без повторного strconv.ParseFloat на каждую публикацию
251: // снапшота (float64-цена парсится один раз, в setLevel, при вставке/
252: // обновлении уровня — здесь она просто переиспользуется как ключ).
253: func (lob *LocalOrderBook) Snapshot(tsMs int64) OrderBookFullSnapshot {
254: 	bidPrices := make([]float64, 0, len(lob.bids))
255: 	for price := range lob.bids {
256: 		bidPrices = append(bidPrices, price)
257: 	}
258: 	sort.Slice(bidPrices, func(i, j int) bool {
259: 		return bidPrices[i] > bidPrices[j] // убывание — лучшая (самая высокая) покупка первая
260: 	})
261: 	bids := make([]OBLevel, 0, len(bidPrices))
262: 	for _, price := range bidPrices {
263: 		lvl := lob.bids[price]
264: 		bids = append(bids, OBLevel{Price: lvl.priceStr, Size: lvl.sizeStr})
265: 	}
266: 
267: 	askPrices := make([]float64, 0, len(lob.asks))
268: 	for price := range lob.asks {
269: 		askPrices = append(askPrices, price)
270: 	}
271: 	sort.Slice(askPrices, func(i, j int) bool {
272: 		return askPrices[i] < askPrices[j] // возрастание — лучшая (самая низкая) продажа первая
273: 	})
274: 	asks := make([]OBLevel, 0, len(askPrices))
275: 	for _, price := range askPrices {
276: 		lvl := lob.asks[price]
277: 		asks = append(asks, OBLevel{Price: lvl.priceStr, Size: lvl.sizeStr})
278: 	}
279: 
280: 	return OrderBookFullSnapshot{T: tsMs, S: lob.symbol, Bids: bids, Asks: asks}
281: }
282: 
283: // =============================================================================
284: // Интеграция с WSClient: инициализация снапшотов и пересинхронизация
285: // =============================================================================
286: 
287: // InitOrderBookSnapshots берёт REST-снапшот для каждого символа и
288: // инициализирует локальные стаканы. Вызывается один раз при старте,
289: // ДО подписки на order_book_update (см. main.go) — если подписаться
290: // раньше снапшота, часть дельт придётся выбросить впустую, ожидая
291: // точку стыковки, что не страшно функционально, но менее эффективно.
292: //
293: // depth — та же глубина, что передаётся в SubscribeOrderBookUpdate —
294: // официальный алгоритм требует совпадения глубины снапшота и подписки
295: // (см. предупреждение в документации Gate.io: "The subscribed level
296: // should match the limit parameter in the REST snapshot").
297: func (c *WSClient) InitOrderBookSnapshots(ctx context.Context, symbols []string, depth int) error {
298: 	if c.restClient == nil {
299: 		return fmt.Errorf("orderbook snapshot: REST-клиент не задан в WSClient")
300: 	}
301: 	c.booksMu.Lock()
302: 	defer c.booksMu.Unlock()
303: 
304: 	for _, symbol := range symbols {
305: 		snap, err := c.restClient.GetOrderBookSnapshot(ctx, symbol, depth)
306: 		if err != nil {
307: 			return fmt.Errorf("orderbook snapshot %s: %w", symbol, err)
308: 		}
309: 		c.books[symbol] = newLocalOrderBook(symbol, snap, depth)
310: 		log.Printf("📖 [orderbook] снапшот получен: %s id=%d bids=%d asks=%d",
311: 			symbol, snap.ID, len(snap.Bids), len(snap.Asks))
312: 	}
313: 	return nil
314: }
315: 
316: // resyncOrderBook пересинхронизирует стакан ОДНОГО символа — вызывается
317: // из handleOrderBook (parser.go) при обнаружении разрыва последовательности.
318: // ctx берётся с коротким таймаутом отдельно от общего жизненного цикла
319: // соединения — пересинхронизация не должна виснуть дольше, чем на разумный
320: // REST-запрос, даже если основной ctx ещё долго не отменится.
321: //
322: // Вызывающий код (parser.go) обязан выставить c.resyncing[symbol]=true
323: // ДО запуска этой горутины — здесь только гарантированный сброс флага по
324: // завершении (через defer), чтобы символ не остался навсегда
325: // заблокированным для будущих resync, даже если REST-запрос упал с ошибкой.
326: func (c *WSClient) resyncOrderBook(symbol string, depth int) {
327: 	defer func() {
328: 		c.booksMu.Lock()
329: 		delete(c.resyncing, symbol)
330: 		c.booksMu.Unlock()
331: 	}()
332: 
333: 	if c.restClient == nil {
334: 		// В отличие от InitOrderBookSnapshots (которая возвращает явную
335: 		// ошибку при том же условии) — здесь функция вызывается через
336: 		// go c.resyncOrderBook(...) и не возвращает ошибку по дизайну
337: 		// (это fire-and-forget горутина). Раньше эта ветка молчала
338: 		// вообще без лога — при отладке "почему стакан не восстановился
339: 		// после разрыва последовательности" разработчик видел бы только
340: 		// то, что флаг resyncing сброшен, без единого объяснения причины
341: 		// в логах. Найдено независимым аудитом (OpenCode + Claude
342: 		// Sonnet 5, 2026-08-10).
343: 		log.Printf("⚠️ orderbook resync %s пропущен: REST-клиент не задан в WSClient", symbol)
344: 		return
345: 	}
346: 	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
347: 	defer cancel()
348: 
349: 	snap, err := c.restClient.GetOrderBookSnapshot(ctx, symbol, depth)
350: 	if err != nil {
351: 		log.Printf("⚠️ orderbook resync %s failed: %v", symbol, err)
352: 		return
353: 	}
354: 	c.booksMu.Lock()
355: 	c.books[symbol] = newLocalOrderBook(symbol, snap, depth)
356: 	c.booksMu.Unlock()
357: 	log.Printf("🔄 [orderbook] пересинхронизация выполнена: %s id=%d", symbol, snap.ID)
358: }

(End of file - total 358 lines)
</content>
```

**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/ws.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/ws.go</path>
<type>file</type>
<content>
1: // Этот файл отвечает за главный цикл чтения сообщений от Gate.io.
2: // Управление соединением — в connection.go, ping/pong и EMA — в pingloop.go,
3: // структуры протокола — в protocol.go, подписки на каналы — в subscribe.go,
4: // разбор конкретных типов рыночных данных — в parser.go.
5: //
6: // ReadLoop специально сделан "тонким": он только читает байты, распаковывает
7: // конверт (WSResponse) и решает, КОМУ отдать Result — сам не занимается
8: // разбором Trade/Candle/OrderBook и т.д., это уже дело parser.go.
9: package gateway
10: 
11: import (
12: 	"context"
13: 	"encoding/json"
14: 	"log"
15: 	"math"
16: 	"time"
17: 
18: 	"github.com/gorilla/websocket"
19: )
20: 
21: // ReadLoop — главный цикл чтения сообщений от Gate.io WebSocket.
22: // Должен запускаться в отдельной горутине (go wsClient.ReadLoop(ctx))
23: // параллельно с RunPingLoop.
24: //
25: // Цикл работает, пока conn.ReadMessage() не вернёт ошибку — это
26: // происходит либо при отмене ctx (плановое завершение), либо при
27: // разрыве соединения (сеть, биржа закрыла коннект и т.д.).
28: func (c *WSClient) ReadLoop(ctx context.Context) {
29: 	signalDone := func() {
30: 		select {
31: 		case c.done <- struct{}{}:
32: 		default:
33: 		}
34: 	}
35: 	for {
36: 		_, raw, err := c.conn.ReadMessage()
37: 		if err != nil {
38: 			if ctx.Err() != nil {
39: 				return
40: 			}
41: 			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
42: 				log.Printf("❌ WS ошибка: %v", err)
43: 			}
44: 			signalDone()
45: 			return
46: 		}
47: 
48: 		var msg WSResponse
49: 		if err := json.Unmarshal(raw, &msg); err != nil {
50: 			log.Printf("⚠️ Не удалось разобрать: %s", string(raw))
51: 			continue
52: 		}
53: 
54: 		// --- Служебные случаи (не рыночные данные) ---
55: 
56: 		if msg.Channel == "futures.pong" {
57: 			// Считаем RTT и обновляем EMA (updateEMA — см. pingloop.go)
58: 			latencyMs := time.Now().UnixMilli() - c.pingTs
59: 			c.updateEMA(latencyMs)
60: 			// Пишем текущую латентность и EMA в Redis
61: 			if c.pub != nil {
62: 				emaMs := int64(math.Round(c.emaLat))
63: 				if err := c.pub.PublishExchangePing(ctx, latencyMs, emaMs); err != nil {
64: 					log.Printf("⚠️ publish exchange_ping failed: err=%v", err)
65: 					c.pub.Metrics.IncDropped()
66: 				}
67: 			}
68: 			continue
69: 		}
70: 		if msg.Error != nil {
71: 			log.Printf("❌ Ошибка биржи: code=%d msg=%s channel=%s",
72: 				msg.Error.Code, msg.Error.Message, msg.Channel)
73: 			continue
74: 		}
75: 		if msg.Event == "subscribe" {
76: 			log.Printf("✅ Подписка подтверждена: channel=%s", msg.Channel)
77: 			continue
78: 		}
79: 
80: 		// --- Рыночные данные: отдаём в parser.go по каналу ---
81: 
82: 		switch msg.Channel {
83: 		case "futures.trades":
84: 			c.handleTrades(ctx, msg.Result)
85: 		case "futures.order_book_update":
86: 			c.handleOrderBook(ctx, msg.Result)
87: 		case "futures.candlesticks":
88: 			c.handleCandles(ctx, msg.Result)
89: 		case "futures.public_liquidates":
90: 			c.handleLiquidations(ctx, msg.Result)
91: 		case "futures.contract_stats":
92: 			c.handleContractStats(ctx, msg.Result)
93: 		}
94: 	}
95: }

(End of file - total 95 lines)
</content>
```

**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go</path>
<type>file</type>
<content>
1: // Этот файл отвечает ТОЛЬКО за поддержание соединения живым (ping/pong)
2: // и за измерение латентности до биржи через EMA.
3: // Здесь нет управления самим соединением (см. connection.go) и нет
4: // разбора рыночных данных (см. ws.go / будущий parser.go).
5: package gateway
6: 
7: import (
8: 	"context"
9: 	"log"
10: 	"time"
11: 
12: 	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
13: )
14: 
15: // emaAlpha — коэффициент сглаживания EMA (экспоненциальной скользящей
16: // средней) латентности, рассчитанный на "окно" из 100 периодов.
17: //
18: // Формула стандартная для EMA: α = 2 / (N + 1)
19: // При N = 100:  α = 2 / 101 ≈ 0.0198
20: //
21: // Смысл: чем МЕНЬШЕ α, тем ПЛАВНЕЕ EMA реагирует на новые значения —
22: // один случайный скачок пинга на 500ms не обвалит показатель EXCH
23: // в TUI, а плавно "размажется" по следующим ~100 замерам.
24: const emaAlpha = 2.0 / (100.0 + 1.0)
25: 
26: // sendPing отправляет ping-сообщение на Gate.io и запоминает момент
27: // отправки — это нужно, чтобы посчитать RTT (round-trip time) при
28: // получении pong в ReadLoop (см. ws.go).
29: func (c *WSClient) sendPing() error {
30: 	// Запоминаем момент отправки в миллисекундах — при получении pong
31: 	// вычтем это значение из времени получения и получим RTT.
32: 	c.pingTs = time.Now().UnixMilli()
33: 	return c.writeJSON(WSRequest{
34: 		Time:    utils.NowUnix(),
35: 		Channel: "futures.ping",
36: 	})
37: }
38: 
39: // RunPingLoop запускает бесконечный цикл ping/pong с периодом interval.
40: // Должен запускаться в отдельной горутине (go wsClient.RunPingLoop(ctx, interval))
41: // параллельно с ReadLoop — иначе цикл будет блокировать чтение сообщений.
42: //
43: // interval берётся из config.yaml (exchange.ping_interval) — раньше был
44: // захардкожен как 10 секунд прямо здесь, теперь можно менять без
45: // пересборки бинарника.
46: //
47: // Цикл завершается по любому из трёх условий:
48: //  1. ctx отменён (например, пришёл SIGTERM) — плановое завершение
49: //  2. c.done просигналил — соединение разорвано где-то ещё (ReadLoop
50: //     обнаружил обрыв) — нет смысла продолжать пинговать мёртвое соединение
51: //  3. sendPing вернул ошибку — соединение, видимо, уже не работает
52: func (c *WSClient) RunPingLoop(ctx context.Context, interval time.Duration) {
53: 	if err := c.sendPing(); err != nil {
54: 		log.Printf("❌ Первый ping не удался: %v", err)
55: 		return
56: 	}
57: 	ticker := time.NewTicker(interval)
58: 	defer ticker.Stop()
59: 	for {
60: 		select {
61: 		case <-ctx.Done():
62: 			return
63: 		case <-c.done:
64: 			return
65: 		case <-ticker.C:
66: 			if err := c.sendPing(); err != nil {
67: 				log.Printf("❌ Ошибка ping: %v", err)
68: 				return
69: 			}
70: 			// Публикуем счётчик пропущенных публикаций тем же ритмом,
71: 			// что и ping — раз в 10 секунд, без отдельного тикера.
72: 			//
73: 			// Осознанное исключение из общего правила "лог + IncDropped()":
74: 			// если сама публикация метрик не удалась, инкремент счётчика
75: 			// здесь создал бы логическую петлю (метрика о провале публикации
76: 			// метрики) — просто логируем и идём дальше.
77: 			if c.pub != nil {
78: 				if err := c.pub.PublishMetrics(ctx); err != nil {
79: 					log.Printf("⚠️ publish metrics failed: %v", err)
80: 				}
81: 			}
82: 		}
83: 	}
84: }
85: 
86: // updateEMA пересчитывает экспоненциальную скользящую среднюю латентности
87: // по формуле: EMA_новое = current × α + EMA_старое × (1 - α)
88: //
89: // При самом первом замере (emaLat ещё не инициализирован, равен нулю)
90: // просто берём текущее значение как стартовую точку — иначе первая EMA
91: // была бы искусственно занижена (0 × (1-α) исказил бы среднее).
92: func (c *WSClient) updateEMA(latencyMs int64) {
93: 	current := float64(latencyMs)
94: 	if c.emaLat == 0 {
95: 		// Первое измерение — инициализируем EMA текущим значением
96: 		c.emaLat = current
97: 	} else {
98: 		// EMA = новое × α + старое × (1 - α)
99: 		c.emaLat = current*emaAlpha + c.emaLat*(1-emaAlpha)
100: 	}
101: }

(End of file - total 101 lines)
</content>
```

---

## Assistant (Build · Claude Sonnet 5 · 5.0s)

_Thinking:_



**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/parser.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/parser.go</path>
<type>file</type>
<content>
1: // Этот файл отвечает ТОЛЬКО за разбор и обработку рыночных данных по
2: // каждому конкретному каналу Gate.io: как только ReadLoop (см. ws.go)
3: // определил, что за канал пришёл — управление передаётся сюда.
4: // Здесь нет чтения из сети (см. connection.go) и нет ping/pong (см. pingloop.go).
5: package gateway
6: 
7: import (
8: 	"context"
9: 	"encoding/json"
10: 	"fmt"
11: 	"log"
12: 	"strings"
13: )
14: 
15: // parseLiquidations разбирает поле Result канала futures.public_liquidates.
16: // Особенность протокола Gate.io: биржа присылает ТО массив ликвидаций,
17: // ТО одиночный объект — в зависимости от того, сколько ликвидаций
18: // произошло за тик. Поэтому сначала пробуем распарсить как массив,
19: // и только если не получилось — как одиночный объект.
20: func parseLiquidations(raw json.RawMessage) ([]Liquidation, error) {
21: 	var liqs []Liquidation
22: 	if err := json.Unmarshal(raw, &liqs); err == nil {
23: 		return liqs, nil
24: 	}
25: 	var liq Liquidation
26: 	err := json.Unmarshal(raw, &liq)
27: 	if err == nil {
28: 		return []Liquidation{liq}, nil
29: 	}
30: 	// Оба варианта парсинга не сработали — оборачиваем именно ошибку
31: 	// разбора как одиночный объект (%w сохраняет её как причину для
32: 	// errors.Is/errors.As), это более информативный вариант из двух:
33: 	// массив редко имеет смысл присылать пустым или единственным
34: 	// элементом, поэтому чаще всего реальная проблема протокола
35: 	// обнаруживается именно на попытке разбора как объекта.
36: 	return nil, fmt.Errorf("не удалось распарсить ликвидацию (ни как массив, ни как объект): %w", err)
37: }
38: 
39: // handleTrades обрабатывает пакет сделок с канала futures.trades.
40: // Внутренние (служебные) сделки биржи — например, авто-делевередж —
41: // пропускаем: это не реальный рыночный поток, публиковать их в Redis
42: // значит засорять данные, на которых потом считает analyzer.
43: func (c *WSClient) handleTrades(ctx context.Context, raw json.RawMessage) {
44: 	var trades []Trade
45: 	if err := json.Unmarshal(raw, &trades); err != nil {
46: 		log.Printf("⚠️ trades parse error: %v", err)
47: 		return
48: 	}
49: 	for _, t := range trades {
50: 		if t.IsInternal {
51: 			continue
52: 		}
53: 		if c.pub != nil {
54: 			if err := c.pub.PublishTrade(ctx, t.Contract, map[string]interface{}{
55: 				"id":    t.ID,
56: 				"price": t.Price,
57: 				"size":  t.Size,
58: 				"ts":    t.CreateTimeMs,
59: 			}); err != nil {
60: 				log.Printf("⚠️ publish trade failed: symbol=%s err=%v", t.Contract, err)
61: 				c.pub.Metrics.IncDropped()
62: 			}
63: 		}
64: 	}
65: }
66: 
67: // handleOrderBook обрабатывает входящее сообщение с канала
68: // futures.order_book_update — это может быть либо ПОЛНЫЙ снапшот
69: // (Full == true, редкий случай — см. protocol.go и обработку в
70: // orderbook.go/ApplyDelta), либо, в подавляющем большинстве случаев,
71: // ИНКРЕМЕНТАЛЬНАЯ дельта. Разница обрабатывается внутри
72: // LocalOrderBook.ApplyDelta — здесь эта деталь не важна, просто
73: // передаём сообщение как есть.
74: //
75: // В отличие от предыдущей версии (которая публиковала сырую дельту
76: // как есть — см. CHECKPOINT.md, раздел 13b), эта версия:
77: //  1. применяет дельту к локально поддерживаемому полному стакану
78: //     (см. orderbook.go, LocalOrderBook.ApplyDelta)
79: //  2. публикует в Redis уже ПОЛНЫЙ стакан после применения — analyzer
80: //     спроектирован читать из market:orderbook:{symbol} именно полный
81: //     снапшот, не дельту (см. CHECKPOINT.md, раздел 13a)
82: //  3. при обнаружении разрыва последовательности запускает
83: //     пересинхронизацию в отдельной горутине — не блокирует ReadLoop
84: //     на время REST-запроса
85: func (c *WSClient) handleOrderBook(ctx context.Context, raw json.RawMessage) {
86: 	var ob OrderBookUpdate
87: 	if err := json.Unmarshal(raw, &ob); err != nil {
88: 		log.Printf("⚠️ order_book_update parse error: %v", err)
89: 		return
90: 	}
91: 
92: 	c.booksMu.Lock()
93: 	lob, exists := c.books[ob.S]
94: 	c.booksMu.Unlock()
95: 
96: 	if !exists {
97: 		// Дельта пришла раньше, чем успел отработать InitOrderBookSnapshots
98: 		// (см. main.go — снапшоты запрашиваются ДО подписки на канал,
99: 		// но сетевые вызовы не мгновенны). Это ожидаемая гонка на старте,
100: 		// не ошибка — просто пропускаем дельту и ждём следующую, снапшот
101: 		// появится очень скоро.
102: 		return
103: 	}
104: 
105: 	applied, needResync := lob.ApplyDelta(ob)
106: 
107: 	if needResync {
108: 		log.Printf("🔄 [orderbook] обнаружен разрыв последовательности: %s — пересинхронизация", ob.S)
109: 		// depth берём из lob.Depth() — реальной глубины, с которой был
110: 		// запрошен уже загруженный снапшот (сохранена в LocalOrderBook
111: 		// при его создании) — столько уровней запросили изначально,
112: 		// столько и запрашиваем заново, глубина не должна "плавать"
113: 		// между вызовами (см. предупреждение в официальной документации
114: 		// Gate.io о необходимости совпадения depth снапшота и level
115: 		// подписки).
116: 		//
117: 		// НЕ путать с len(ob.Bids)/len(ob.Asks) — это длина ТЕКУЩЕЙ
118: 		// ВХОДЯЩЕЙ ДЕЛЬТЫ (обычно всего несколько изменившихся уровней,
119: 		// не полная глубина стакана). Именно так этот код был написан
120: 		// раньше и содержал баг — найдено независимым аудитом
121: 		// (OpenCode + Claude Sonnet 5, 2026-08-10): подмена переменных
122: 		// ob (дельта) и lob (загруженный стакан) с похожими именами.
123: 		depth := lob.Depth()
124: 
125: 		if !c.tryStartResync(ob.S) {
126: 			// Resync для этого символа уже идёт — не запускаем ещё один
127: 			// параллельный REST-запрос (см. tryStartResync).
128: 			return
129: 		}
130: 		go c.resyncOrderBook(ob.S, depth)
131: 		return
132: 	}
133: 
134: 	if !applied {
135: 		// Ждём точку стыковки со свежим снапшотом — см. комментарий
136: 		// в LocalOrderBook.ApplyDelta, это ожидаемо в первые мгновения
137: 		// после инициализации, не ошибка.
138: 		return
139: 	}
140: 
141: 	if c.pub != nil {
142: 		snapshot := lob.Snapshot(ob.T)
143: 		if err := c.pub.PublishOrderBook(ctx, ob.S, snapshot); err != nil {
144: 			log.Printf("⚠️ publish order_book failed: symbol=%s err=%v", ob.S, err)
145: 			c.pub.Metrics.IncDropped()
146: 		}
147: 	}
148: }
149: 
150: // parseSymbolFromCandleName извлекает символ из поля Name канала
151: // futures.candlesticks. Gate.io шлёт Name в формате
152: // "{timeframe}_{symbol}", например "1m_BTC_USDT" — отрезаем префикс
153: // таймфрейма до первого "_". Если разделитель не найден — возвращаем
154: // name как есть (защита от неожиданного формата, лучше опубликовать
155: // под странным, но не пустым символом, чем молча потерять данные).
156: //
157: // Вынесена в отдельную функцию (не инлайн внутри handleCandles) именно
158: // для тестируемости — раньше здесь был захардкоженный name[3:]
159: // (предполагал ровно 3 символа префикса, как у "1m_"), который молча
160: // ломался бы для таймфреймов с более длинным префиксом ("15m_", "30m_").
161: // Найдено независимым аудитом (OpenCode + Claude Sonnet 5, 2026-08-10).
162: // Сейчас bot подписывается только на 1m (см. SubscribeCandlesticks в
163: // subscribe.go), поэтому баг не проявлялся на практике, но разбор по
164: // разделителю устойчив к префиксу любой длины на будущее.
165: func parseSymbolFromCandleName(name string) string {
166: 	if idx := strings.IndexByte(name, '_'); idx >= 0 {
167: 		return name[idx+1:]
168: 	}
169: 	return name
170: }
171: 
172: // handleCandles обрабатывает пакет свечей с канала futures.candlesticks.
173: // Публикуем только ЗАКРЫТЫЕ свечи (candle.Window == true) — иначе на
174: // каждое промежуточное обновление внутри текущей минуты мы бы писали
175: // в Redis недостроенную свечу, и analyzer считал бы по неполным данным.
176: func (c *WSClient) handleCandles(ctx context.Context, raw json.RawMessage) {
177: 	var candles []Candle
178: 	if err := json.Unmarshal(raw, &candles); err != nil {
179: 		log.Printf("⚠️ candlesticks parse error: %v", err)
180: 		return
181: 	}
182: 	for _, candle := range candles {
183: 		if candle.Window && c.pub != nil {
184: 			symbol := parseSymbolFromCandleName(candle.Name)
185: 			if err := c.pub.PublishCandle(ctx, symbol, candle); err != nil {
186: 				log.Printf("⚠️ publish candle failed: symbol=%s err=%v", symbol, err)
187: 				c.pub.Metrics.IncDropped()
188: 			}
189: 		}
190: 	}
191: }
192: 
193: // handleLiquidations обрабатывает ликвидации с канала futures.public_liquidates.
194: func (c *WSClient) handleLiquidations(ctx context.Context, raw json.RawMessage) {
195: 	liqs, err := parseLiquidations(raw)
196: 	if err != nil {
197: 		log.Printf("⚠️ liquidates parse error: %v", err)
198: 		return
199: 	}
200: 	for _, liq := range liqs {
201: 		if c.pub != nil {
202: 			if err := c.pub.PublishLiquidation(ctx, liq.Contract, map[string]interface{}{
203: 				"price":   liq.Price,
204: 				"size":    liq.Size,
205: 				"time_ms": liq.TimeMs,
206: 			}); err != nil {
207: 				log.Printf("⚠️ publish liquidation failed: symbol=%s err=%v", liq.Contract, err)
208: 				c.pub.Metrics.IncDropped()
209: 			}
210: 		}
211: 	}
212: }
213: 
214: // handleContractStats обрабатывает статистику контракта с канала
215: // futures.contract_stats (OI, LSR и т.д. — раз в минуту).
216: func (c *WSClient) handleContractStats(ctx context.Context, raw json.RawMessage) {
217: 	var stats ContractStats
218: 	if err := json.Unmarshal(raw, &stats); err != nil {
219: 		log.Printf("⚠️ contract_stats parse error: %v", err)
220: 		return
221: 	}
222: 	if c.pub != nil {
223: 		if err := c.pub.PublishContractStats(ctx, stats.Contract, stats); err != nil {
224: 			log.Printf("⚠️ publish contract_stats failed: symbol=%s err=%v", stats.Contract, err)
225: 			c.pub.Metrics.IncDropped()
226: 		}
227: 	}
228: }

(End of file - total 228 lines)
</content>
```

---

## Assistant (Build · Claude Sonnet 5 · 5.1s)

Теперь найдём место реконнекта в main.go, чтобы понять, что происходит с WSClient при реконнекте.

**Tool: bash**

**Input:**
```json
{
  "command": "grep -rn \"NewWSClient\\|ResetDone\\|Connect(\\|ReadLoop\\|RunPingLoop\\|InitOrderBookSnapshots\\|\\.Close()\" /home/tda/code/dtrader/dtrader-6/bot/cmd/ /home/tda/code/dtrader/dtrader-6/bot/internal/ --include=\"*.go\" | grep -v _test.go | grep -v \"gateway/\""
}
```

**Output:**
```
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:39:	defer pub.Close()
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:101:	wsClient := gateway.NewWSClient(cfg.Exchange.WsURL, cfg.Secrets.APIKey, cfg.Secrets.APISecret, pub, client)
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:112:		// InitOrderBookSnapshots — ДО подписки на order_book_update.
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:119:		err := wsClient.InitOrderBookSnapshots(snapCtx, cfg.Symbols, cfg.Orderbook.Depth)
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:140:		wsClient.ResetDone()
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:142:		if err := wsClient.Connect(ctx); err != nil {
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:155:		go wsClient.ReadLoop(ctx)
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:156:		go wsClient.RunPingLoop(ctx, cfg.Exchange.PingIntervalDuration())
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:160:			wsClient.Close()
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:176:			wsClient.Close()
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:187:	wsClient.Close()
/home/tda/code/dtrader/dtrader-6/bot/internal/publisher/metrics.go:20:	// что счётчик инкрементируется из горутины ReadLoop, а читается
/home/tda/code/dtrader/dtrader-6/bot/internal/publisher/metrics.go:21:	// из горутины RunPingLoop раз в 10 секунд — это классический случай
/home/tda/code/dtrader/dtrader-6/bot/internal/publisher/redis.go:59:	return p.rdb.Close()
/home/tda/code/dtrader/dtrader-6/bot/internal/publisher/redis.go:142:// публикаций в Redis. Вызывается из RunPingLoop раз в 10 секунд —
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:135:	defer file.Close()

```

---

## Assistant (Build · Claude Sonnet 5 · 3.6s)

**Tool: read**

**Input:**
```json
{
  "filePath": "/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go"
}
```

**Output:**
```
<path>/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go</path>
<type>file</type>
<content>
1: package main
2: 
3: import (
4: 	"context"
5: 	"fmt"
6: 	"log"
7: 	"os"
8: 	"os/signal"
9: 	"syscall"
10: 	"time"
11: 
12: 	"github.com/Dmitriy-495/dtrader-6/bot/internal/config"
13: 	"github.com/Dmitriy-495/dtrader-6/bot/internal/gateway"
14: 	"github.com/Dmitriy-495/dtrader-6/bot/internal/publisher"
15: )
16: 
17: func main() {
18: 	fmt.Println("🚀 DTrader 6 Bot запускается...")
19: 
20: 	cfg, err := config.Load("config.yaml")
21: 	if err != nil {
22: 		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
23: 	}
24: 	fmt.Printf("✅ Конфиг загружен: %s (%s)\n", cfg.App.Name, cfg.App.Env)
25: 	fmt.Printf("   Биржа:   %s\n", cfg.Exchange.Name)
26: 	fmt.Printf("   Символы: %v\n", cfg.Symbols)
27: 	fmt.Printf("   Redis:   %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
28: 
29: 	pub := publisher.New(
30: 		cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password,
31: 		cfg.Storage.Trades, cfg.Storage.Liquidations, cfg.Storage.Candles1m,
32: 	)
33: 	pingCtx, cancelPing := context.WithTimeout(context.Background(), gateway.RequestTimeout)
34: 	if err := pub.Ping(pingCtx); err != nil {
35: 		log.Fatalf("❌ Redis недоступен: %v", err)
36: 	}
37: 	cancelPing()
38: 	fmt.Printf("✅ Redis подключён: %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
39: 	defer pub.Close()
40: 
41: 	newCtx := func() (context.Context, context.CancelFunc) {
42: 		return context.WithTimeout(context.Background(), gateway.RequestTimeout)
43: 	}
44: 
45: 	client := gateway.NewClient(cfg.Secrets.APIKey, cfg.Secrets.APISecret, cfg.Exchange.RestURL)
46: 
47: 	restPingCtx, cancelRestPing := newCtx()
48: 	contractName, err := client.Ping(restPingCtx)
49: 	cancelRestPing()
50: 	if err != nil {
51: 		log.Fatalf("❌ Ping не удался: %v", err)
52: 	}
53: 	fmt.Printf("✅ Биржа доступна: %s\n", contractName)
54: 
55: 	balanceCtx, cancelBalance := newCtx()
56: 	account, err := client.GetUnifiedBalance(balanceCtx)
57: 	cancelBalance()
58: 	if err != nil {
59: 		log.Fatalf("❌ Ошибка получения баланса: %v", err)
60: 	}
61: 	fmt.Printf("✅ Баланс: %s USDT | Маржа: %s USDT | Плечо: x%s\n",
62: 		account.UnifiedAccountTotal,
63: 		account.TotalAvailableMargin,
64: 		account.Leverage,
65: 	)
66: 
67: 	// Пишем баланс в Redis — ws-server транслирует в TUI
68: 	balPubCtx, cancelBalPub := newCtx()
69: 	if err := pub.PublishBalance(balPubCtx,
70: 		account.UnifiedAccountTotal,
71: 		account.TotalAvailableMargin,
72: 		account.Leverage,
73: 	); err != nil {
74: 		log.Printf("⚠️ Не удалось записать баланс в Redis: %v", err)
75: 	}
76: 	cancelBalPub()
77: 
78: 	posCtx, cancelPos := newCtx()
79: 	positions, err := client.GetPositions(posCtx)
80: 	cancelPos()
81: 	if err != nil {
82: 		log.Fatalf("❌ Ошибка получения позиций: %v", err)
83: 	}
84: 	if len(positions) == 0 {
85: 		fmt.Println("✅ Открытых позиций нет")
86: 	} else {
87: 		fmt.Printf("✅ Открытые позиции (%d):\n", len(positions))
88: 		for i, p := range positions {
89: 			direction := "LONG 📈"
90: 			if p.Size < 0 {
91: 				direction = "SHORT 📉"
92: 			}
93: 			fmt.Printf("   [%d] %s %s | Вход: %s | PnL: %s\n",
94: 				i+1, p.Contract, direction, p.EntryPrice, p.UnrealisedPnl)
95: 		}
96: 	}
97: 
98: 	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
99: 	defer cancel()
100: 
101: 	wsClient := gateway.NewWSClient(cfg.Exchange.WsURL, cfg.Secrets.APIKey, cfg.Secrets.APISecret, pub, client)
102: 
103: 	// reconnectInterval — пауза перед повторной попыткой подключения,
104: 	// берётся из config.yaml (exchange.reconnect_interval), а не
105: 	// захардкожена — используется во всех трёх местах реконнекта ниже.
106: 	reconnectInterval := cfg.Exchange.ReconnectIntervalDuration()
107: 
108: 	subscribeAll := func() error {
109: 		if err := wsClient.SubscribeTrades(cfg.Symbols); err != nil {
110: 			return fmt.Errorf("trades: %w", err)
111: 		}
112: 		// InitOrderBookSnapshots — ДО подписки на order_book_update.
113: 		// Официальный алгоритм Gate.io требует базовый REST-снапшот перед
114: 		// тем, как доверять входящим WS-дельтам (см. orderbook.go). Делаем
115: 		// это при КАЖДОМ вызове subscribeAll, то есть при каждом реконнекте
116: 		// тоже — старое состояние LocalOrderBook из прошлого соединения не
117: 		// годится для нового потока дельт (свои U/u id, своя нумерация).
118: 		snapCtx, cancelSnap := context.WithTimeout(context.Background(), gateway.RequestTimeout)
119: 		err := wsClient.InitOrderBookSnapshots(snapCtx, cfg.Symbols, cfg.Orderbook.Depth)
120: 		cancelSnap()
121: 		if err != nil {
122: 			return fmt.Errorf("orderbook snapshots: %w", err)
123: 		}
124: 		if err := wsClient.SubscribeOrderBookUpdate(cfg.Symbols, cfg.Orderbook.Depth); err != nil {
125: 			return fmt.Errorf("order_book_update: %w", err)
126: 		}
127: 		if err := wsClient.SubscribeCandlesticks(cfg.Symbols); err != nil {
128: 			return fmt.Errorf("candlesticks: %w", err)
129: 		}
130: 		if err := wsClient.SubscribePublicLiquidates(cfg.Symbols); err != nil {
131: 			return fmt.Errorf("public_liquidates: %w", err)
132: 		}
133: 		if err := wsClient.SubscribeContractStats(cfg.Symbols); err != nil {
134: 			return fmt.Errorf("contract_stats: %w", err)
135: 		}
136: 		return nil
137: 	}
138: 
139: 	for {
140: 		wsClient.ResetDone()
141: 
142: 		if err := wsClient.Connect(ctx); err != nil {
143: 			if ctx.Err() != nil {
144: 				break
145: 			}
146: 			log.Printf("❌ WS коннект не удался: %v — повтор через %s", err, reconnectInterval)
147: 			select {
148: 			case <-ctx.Done():
149: 				goto shutdown
150: 			case <-time.After(reconnectInterval):
151: 				continue
152: 			}
153: 		}
154: 
155: 		go wsClient.ReadLoop(ctx)
156: 		go wsClient.RunPingLoop(ctx, cfg.Exchange.PingIntervalDuration())
157: 
158: 		if err := subscribeAll(); err != nil {
159: 			log.Printf("❌ Ошибка подписки: %v — реконнект через %s", err, reconnectInterval)
160: 			wsClient.Close()
161: 			select {
162: 			case <-ctx.Done():
163: 				goto shutdown
164: 			case <-time.After(reconnectInterval):
165: 				continue
166: 			}
167: 		}
168: 
169: 		log.Println("✅ Бот запущен! Данные пишутся в Redis.")
170: 
171: 		select {
172: 		case <-ctx.Done():
173: 			goto shutdown
174: 		case <-wsClient.Done():
175: 			log.Printf("🔄 WS разорван. Реконнект через %s...", reconnectInterval)
176: 			wsClient.Close()
177: 			select {
178: 			case <-ctx.Done():
179: 				goto shutdown
180: 			case <-time.After(reconnectInterval):
181: 			}
182: 		}
183: 	}
184: 
185: shutdown:
186: 	fmt.Println("\n👋 Завершение работы...")
187: 	wsClient.Close()
188: }

(End of file - total 188 lines)
</content>
```

---

## Assistant (Build · Claude Sonnet 5 · 36.1s)

_Thinking:_

I'm zeroing in on the core issue: the wsClient instance persists across reconnects without being recreated, which means the books and resyncing maps retain their state even when the connection drops and reconnects. This creates a potential race condition if a resync operation is still running in the background when a reconnect happens.

When reconnect occurs, InitOrderBookSnapshots locks the booksMu and replaces each symbol's LocalOrderBook with a fresh snapshot aligned to the new connection's stream numbering. But the resyncing map never gets cleared, so an old resyncOrderBook goroutine from the previous connection could still be executing and writing to the books map while the new snapshot initialization is happening—creating a data race where stale resync updates overwrite the fresh snapshot state. The real issue is that resyncOrderBook fetches a fresh REST snapshot with its own timestamp, so the data itself isn't stale, but it creates a nondeterministic race condition where the old connection's resync might complete after the new connection's InitOrderBookSnapshots and overwrite the fresh snapshot with a different LocalOrderBook instance that has synced=false and an outdated lastUpdateID. The `defer` statement clears the `resyncing` flag, but there's a critical gap: `InitOrderBookSnapshots` never touches this map at all, so if the flag was set before the connection dropped, it persists across the reconnect. When the new connection starts receiving deltas that need resyncing, `handleOrderBook` checks `resyncing[symbol]` and finds it still true from the stale goroutine, blocking any new resync attempt.

This means the new session silently suppresses its own resync requests, thinking one is already in progress for the old session. The order book can get stuck unsynced since `ApplyDelta` keeps returning false but `tryStartResync` never fires, leaving the book in limbo until the old goroutine finally times out and clears the flag—only then can the next delta trigger a fresh resync.

Now I'm wondering whether the old `resyncOrderBook`'s context even gets canceled when the connection drops, since it's created with `context.WithTimeout(context.Background())` completely independent of the WebSocket lifecycle.

So the real issue is that `c.resyncing` never gets reset on reconnect — it's only initialized once in `NewWSClient`, and the reconnect logic doesn't touch it, meaning the flag stays stuck in whatever state it was in before the disconnect.

If a resync was in-flight when the connection dropped, its goroutine survives the reconnect because it's running on its own `context.Background()` with its own timeout, completely independent of the main context or the `c.done` channel. This orphaned goroutine can then corrupt the freshly-initialized order book from the new connection by writing a stale snapshot to it, or it can keep the `resyncing` flag locked for that symbol, blocking any legitimate resync attempts from the new session until the old goroutine finally times out — which could be several seconds, causing repeated deltas to fail during that window.

This is a genuine race condition: resync goroutines aren't cancelled or waited for during reconnect, and the shared state (the books map and resyncing flags) isn't reset atomically with the reconnect, so old and new generations of data can interleave. The websocket connection itself gets closed in the reconnect flow, but that doesn't stop the stale goroutine's REST call since it's independent of the connection.…

**Tool: bash**

**Input:**
```json
{
  "command": "grep -n \"RequestTimeout\\|requestTimeout\\|ReconnectInterval\" /home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go /home/tda/code/dtrader/dtrader-6/bot/internal/config/*.go 2>/dev/null"
}
```

**Output:**
```
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go:8:	// requestTimeout — таймаут для всех HTTP запросов к Gate.io.
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go:10:	// Используется в NewClient и в main.go через gateway.RequestTimeout.
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go:11:	requestTimeout = 10 * time.Second
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go:20:// RequestTimeout экспортируемая константа для использования в main.go.
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/constants.go:23:const RequestTimeout = requestTimeout
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:33:	// ReconnectInterval — пауза перед переподключением при разрыве WS,
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:35:	// в коде не нужно — берите ReconnectIntervalDuration() (метод ниже),
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:37:	ReconnectInterval string `yaml:"reconnect_interval"`
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:46:	// что кто-то поменяет строку ReconnectInterval в рантайме, а разобранное
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:52:// ReconnectInterval возвращает паузу перед переподключением при разрыве
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:56:func (e ExchangeConfig) ReconnectIntervalDuration() time.Duration {
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:168:// pingDur), доступные снаружи через ReconnectIntervalDuration() и
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:173:	reconnectDur, err := time.ParseDuration(c.Exchange.ReconnectInterval)
/home/tda/code/dtrader/dtrader-6/bot/internal/config/config.go:176:			c.Exchange.ReconnectInterval, err)

```

---

## Assistant (Build · Claude Sonnet 5 · 18.2s)

_Thinking:_

I'm checking the config.yaml defaults for RequestTimeout and reconnect_interval to understand the timing context, then moving on to analyze the second race condition between the ping loop and read loop. The sendPing function in pingloop.go updates the ping timestamp without holding a mutex, which creates a potential data race since it's running in its own goroutine.

Now I'm seeing that ReadLoop reads this same timestamp field when handling pong messages, and since it's an unprotected int64 being accessed from two different goroutines, the Go race detector would flag this as undefined behavior — even though x86 hardware might handle aligned int64 atomically, the Go memory model requires explicit synchronization for happens-before guarantees. I'm also checking whether emaLat has similar issues, since it's only written by ReadLoop so it shouldn't have a race there. The Go race detector would flag this as a data race on the unsynchronized write and read across goroutines, which violates the memory model regardless of it being "just a timestamp." On 32-bit systems especially, an int64 read could capture a partially-updated value since atomicity isn't guaranteed without the atomic package, potentially returning garbage data mid-update.…

**Tool: bash**

**Input:**
```json
{
  "command": "grep -rn \"pingTs\\|emaLat\" /home/tda/code/dtrader/dtrader-6/bot/internal/gateway/*.go"
}
```

**Output:**
```
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go:20:// Поля pingTs и emaLat физически хранятся здесь (структура одна на весь
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go:34:	pingTs int64   // timestamp последнего отправленного ping (unix ms) — используется в pingloop.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go:35:	emaLat float64 // EMA латентности (ms) — используется в pingloop.go
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:32:	c.pingTs = time.Now().UnixMilli()
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:89:// При самом первом замере (emaLat ещё не инициализирован, равен нулю)
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:94:	if c.emaLat == 0 {
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:96:		c.emaLat = current
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:99:		c.emaLat = current*emaAlpha + c.emaLat*(1-emaAlpha)
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/ws.go:58:			latencyMs := time.Now().UnixMilli() - c.pingTs
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/ws.go:62:				emaMs := int64(math.Round(c.emaLat))

```

---

## Assistant (Build · Claude Sonnet 5 · 54.0s)

_Thinking:_

I've confirmed that `pingTs` is being written from one goroutine in the ping loop and read from a different goroutine in the WebSocket read loop without any synchronization—that's a genuine data race. Meanwhile, `emaLat` stays contained within the read loop goroutine where it's only updated and accessed in the same execution path, so there's no race condition there.

Now I'm checking whether `c.conn` itself is racy. The initial connection assignment happens before the read loop goroutine even starts, so that's safe via happens-before ordering. But on reconnect, I need to verify whether the old read loop goroutine might still be blocked on `conn.ReadMessage()` when `Connect()` runs again and overwrites `c.conn`. Looking at the flow: when the read loop hits an error and calls `signalDone()`, it returns immediately after, so the goroutine should be fully exited before the main loop unblocks and calls `Connect()` again.

However, there's a subtle timing window where the old goroutine might not have been fully scheduled to exit yet when the new `Connect()` writes to `c.conn`. But this doesn't actually cause a functional race because once the read loop encounters an error, it stops reading from `c.conn` — it doesn't access the field again after that point, just returns.

The more pressing concern is `writeMu` — while `RunPingLoop` locks the mutex around `WriteJSON`, the initial read of the `c.conn` pointer itself isn't protected, so there's an unsynchronized access to the field when calling the method on it.

However, the program structure saves us here: `Connect()` finishes and returns to the main goroutine before `ReadLoop` and `RunPingLoop` are spawned, which creates a happens-before guarantee via the goroutine start statement. This means the write to `c.conn` in `Connect()` is guaranteed to be visible to the new goroutines when they start, so there's no race on the initial assignment during each reconnect cycle.

The trickier case is what happens when `Connect()` writes to `c.conn` again on a subsequent reconnect while the previous iteration's goroutines might still be running. The old `ReadLoop` should exit quickly via the non-blocking `signalDone()` send, and `RunPingLoop` should exit when it hits the `<-c.done` case in its select statement. Since `ResetDone()` creates a new channel in the main goroutine on the next loop iteration (after the old goroutines have already exited), the timing works out safely. Actually, I'm realizing there's a race condition here: both the main goroutine and RunPingLoop are trying to receive from the same buffered done channel, but only one of them can actually get that single struct{} value. Whichever one receives it first will unblock, while the other gets stuck waiting forever. This is a separate issue from the ping/pong race, but it's worth flagging since it breaks the RunPingLoop lifecycle after a disconnect. Both goroutines are competing to receive from the same single-buffered channel, so whichever one gets scheduled first will consume the disconnect signal while the other remains blocked or unaware—this is a genuine race condition where it's unpredictable whether the main loop or the ping loop actually observes the done signal.

If main consumes it, reconnection proceeds normally, but RunPingLoop continues obliviously with the closed connection until the next ticker fires and sendPing() fails on the stale connection, causing it to log the error and exit. There's a dangerous window of up to the ping interval where RunPingLoop doesn't realize the connection is dead and could attempt writes on an already-closed connection object.

However, if RunPingLoop drains the done signal first, it exits cleanly but main never receives from `c.done` and gets stuck forever in its select statement waiting for either a context cancellation or the wsClient.Done() signal that will never come again—since ResetDone() is never called while main is blocked, this creates a potential deadlock where the program hangs indefinitely unless the shutdown context fires. The race condition between which goroutine consumes the done signal first determines whether the code self-heals or deadlocks.

This is directly answering the user's second question about whether there's a race between PING/PONG and the ReadLoop—and the answer is yes, there's a critical race on the single-buffered done channel between main's select and RunPingLoop's select, with serious consequences depending on scheduling. The select statements in both RunPingLoop and main are both waiting on the done channel, confirming this is a genuine race condition between multiple consumers of the same signal. This is the critical finding for Q2—it directly addresses whether the ping/pong mechanism is properly protected against the ReadLoop's done signal, and it's exactly the kind of synchronization issue that could cause unpredictable behavior during reconnection. The real issue is that `signalDone` sends a single value instead of closing the channel, which only works for one receiver but the code has two. Closing the channel would broadcast…

**Tool: bash**

**Input:**
```json
{
  "command": "grep -rn \"c\\.done\\|wsClient\\.Done\\|\\.Done()\" /home/tda/code/dtrader/dtrader-6/bot/internal/gateway/*.go /home/tda/code/dtrader/dtrader-6/bot/cmd/*.go"
}
```

**Output:**
```
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go:124:	return c.done
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/connection.go:131:	c.done = make(chan struct{}, 1)
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/orderbook_test.go:251:			defer wg.Done()
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:49://  2. c.done просигналил — соединение разорвано где-то ещё (ReadLoop
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:61:		case <-ctx.Done():
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/pingloop.go:63:		case <-c.done:
/home/tda/code/dtrader/dtrader-6/bot/internal/gateway/ws.go:31:		case c.done <- struct{}{}:
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:148:			case <-ctx.Done():
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:162:			case <-ctx.Done():
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:172:		case <-ctx.Done():
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:174:		case <-wsClient.Done():
/home/tda/code/dtrader/dtrader-6/bot/cmd/main.go:178:			case <-ctx.Done():

```

---

## Assistant (Build · Claude Sonnet 5 · 123.0s)

_Thinking:_

I've confirmed there are two receivers competing for the same buffered channel value from `wsClient.Done()` — one in pingloop.go and another in main.go — while only a single producer sends once. This is a classic race condition with a single-value channel and multiple consumers. Now I need to finalize the analysis with specific file references, confidence levels, and impact assessment, plus verify whether main.go's `wsClient.Close()` also triggers anything on the done channel.

Close() only sends the close frame and closes the connection without touching the done channel. I should also check if there's any generation counter or guard mechanism for resync/books that I might have missed, and quickly review subscribe.go for context. I've confirmed that InitOrderBookSnapshots never clears the resyncing map — it only overwrites the books map for each configured symbol. The reconnect sequence in main.go is clear: wsClient.Done() fires, logs, closes, waits, resets, connects, spawns read and ping loops, then calls subscribeAll() and InitOrderBookSnapshots, which locks booksMu and overwrites the books map for every symbol. If a symbol had resyncing[X]=true at disconnect and its resync goroutine is still running, there's a potential issue with the order of operations.

The stale resync goroutine runs with its own context (context.Background() with a 10-second timeout) completely independent of the reconnect happening in the main goroutine. Both (a) the stale resync writing to c.books[X] and (b) InitOrderBookSnapshots writing to c.books[X] are racing to acquire booksMu.Lock(), so whichever acquires the lock last wins — the ordering is indeterminate. If the stale resync writes after the fresh reconnect snapshot, the newly initialized orderbook gets overwritten with REST data from the previous session, which resets synced=false and causes new-session deltas to be treated as if they're filling an old gap.

Actually, both REST snapshots query the same live endpoint and get consistent global update IDs since Gate.io's orderbook ID sequence is exchange-global, not per-connection, so the overwrite isn't technically "wrong data" — but it's wasteful (extra REST call) and more critically it resets the synced flag again, forcing the local orderbook to re-stitch with incoming WS deltas and potentially delaying publishing if multiple resyncs interleave.

The bigger issue is that resyncing[X] stays locked (blocking legitimate tryStartResync calls) for the entire RequestTimeout window (up to 10 seconds if REST is slow), and during this period the new session's deltas might also trigger needResync=true, which is likely right after a fresh InitOrderBookSnapshots since the first delta needs to find its stitching point.

After InitOrderBookSnapshots, synced=false, and ApplyDelta checks whether the first update ID creates a gap larger than acceptable — if the gap is manageable, it just waits without triggering resync, but a busy market with network delays could still produce a scenario where resync is needed.

The race condition and the resyncing flag not resetting across reconnects are the two solid findings I can present with high confidence on the code-level issue and medium-high confidence on practical impact, though the actual severity depends on timing and scheduling.

There's also the resyncOrderBook goroutine itself — it's never explicitly cancelled or awaited during reconnect or shutdown, with no WaitGroup or context tied to the connection lifecycle, so during graceful shutdown it could still be running with its own background context that isn't cancelled by the main context. This is a secondary point about goroutine lifecycle management during reconnects. resyncOrderBook creates a fresh context with a timeout, clears the resyncing flag in its defer block, and updates the books map at line 355. In main.go, the wsClient is instantiated once outside the loop, but subscribeAll—which calls InitOrderBookSnapshots—runs on every reconnect iteration, while ResetDone only resets the done channel with no equivalent reset for books or resyncing. The parser triggers resyncOrderBook asynchronously when handling order book updates, guarded by a tryStart check.

For the race condition on pingTs: it's written by RunPingLoop (started at line 156) and read by ReadLoop (started at line 155) with no mutex or atomic protection—only writeMu guards connection writes, not this field. The done channel is buffered with capacity 1, created in connection.go, and signaled once by ReadLoop's error handler, but both RunPingLoop and the main loop select-receive on it, creating a potential issue with multiple consumers on the same channel.

The worst case is if RunPingLoop wins the race and consumes the done signal—main's select at line 174 never fires, causing an infinite hang in the reconnect loop until process-level termination, silently stopping WS data processing without triggering reconnect. If main wins instead, RunPingLoop eventually self-terminates when the next sendPing fails on the broken connection, bounded by the ping interval delay, which is less severe but still leaves a window where it might attempt writes.

There's also a deeper race: if the old RunPingLoop goroutine misses the done signal and main closes the connection before waiting for reconnectInterval, the stale goroutine could still be alive when the reconnect loop quickly creates a new connection and reassigns c.conn—now the old RunPingLoop would attempt WriteJSON on a stale connection object while a fresh one is already in use, creating concurrent access to different connection instances. we've got a genuine goroutine leak where the old RunPingLoop never exits because it's waiting on the shared root context that only cancels on SIGTERM, and since it reads the current c.conn field rather than a cached reference, it can accidentally start sending pings on the new connection even after being replaced, creating a zombie goroutine that keeps running indefinitely. ating writes across generations. This is solid logical reasoning grounded in the code structure itself, not dependent on test execution. Now I'm organizing the findings into a clear final response covering both questions with specific file references and confidence levels — starting with the connection lifecycle behavior and the orderbook resync patterns.

The resyncing map persists across reconnects since it's initialized once in the client constructor and never cleared, so any in-flight resync flags survive the disconnect. The resyncOrderBook goroutine operates independently with its own background context and timeout rather than being tied to the connection lifecycle, meaning it continues running even after a reconnect happens. This creates a window where stale resync operations can interfere with the new connection generation. ait, if the stale resyncing flag from the old generation doesn't clear in time, the new connection's handleOrderBook will skip starting a fresh resync and leave the order book stuck unsynchronized for up to 10 seconds — confidence is high since this traces directly through the code paths, and the impact is medium since it self-heals but causes temporary data loss and latency during that window.

There's also a fundamental design gap: no generation counter or cross-generation guard exists to invalidate stale goroutines spawned under a previous connection, which compounds the issue.

On the ping/pong side, I'm seeing a data race on the pingTs field — it's written by the RunPingLoop goroutine and read by the ReadLoop goroutine without any synchronization, just a plain int64 with no mutex or atomic operations. The writeMu only protects the actual conn.Write calls, not this timestamp field, which is a textbook data race that the Go race detector would flag. The real problem is that `done` is a buffered channel with capacity 1, but two separate goroutines (RunPingLoop and main's reconnect loop) are both trying to receive from it. Since Go delivers the single buffered send to only one receiver nondeterministically, if main loses that race, its reconnect logic hangs indefinitely and the bot stops processing WebSocket data without attempting recovery.

Conversely, if RunPingLoop loses the race, it won't receive the exit signal and only terminates when its next ping write fails. But because the connection field gets reassigned during reconnects, the stale RunPingLoop can end up successfully writing pings to the new connection — creating a goroutine leak where two RunPingLoop instances run concurrently, both racing to update the ping timestamp and publish metrics, which corrupts RTT measurements and compounds with each reconnect cycle.

The core issue is that the done channel provides no real protection; it assumes a single consumer when the architecture allows multiple generations of RunPingLoop to coexist. I need to clarify this race condition and verify whether main's reconnection logic runs exactly once per connection generation.…

# Аудит bot/internal/gateway — жизненный цикл соединения и ping/pong race

## Вопрос 1: Жизненный цикл WSClient при реконнекте

**Ключевой факт:** `WSClient` — это один и тот же объект на все реконнекты (создаётся один раз, `main.go:101`, вне цикла реконнекта `main.go:139-183`). Значит, состояние `books`/`resyncing` либо переживает реконнект, либо должно явно сбрасываться. Проверил, как это на самом деле сделано.

### Находка 1.1 — `books` пересоздаются корректно
`subscribeAll()` (`main.go:108-137`) вызывается на **каждой** итерации цикла реконнекта и внутри вызывает `InitOrderBookSnapshots` (`main.go:119` → `orderbook.go:297-314`), которая под `booksMu` (`orderbook.go:301`) перезаписывает `c.books[symbol]` свежим `LocalOrderBook`. Это то, что нужно — старая нумерация `lastUpdateID` от прошлого WS-сеанса выбрасывается.
**Уверенность:** высокая. **Влияние:** это работает как задумано, проблемы нет.

### Находка 1.2 — `resyncing` НИКОГДА не сбрасывается на реконнекте
`resyncing` инициализируется один раз в `NewWSClient` (`connection.go:117`). `ResetDone()` (`connection.go:130-132`) трогает только канал `done`. `InitOrderBookSnapshots` тоже не трогает `c.resyncing` (`orderbook.go:297-314`). Если в момент обрыва соединения для какого-то символа уже стоял флаг `c.resyncing[symbol]=true` (выставлен в `parser.go:125` через `tryStartResync`), этот флаг **переживает реконнект как есть**.

**Уверенность:** высокая (прямое чтение кода). **Влияние:** см. 1.3–1.4 ниже — именно из-за этого флага возникают два эффекта.

### Находка 1.3 — фоновая горутина `resyncOrderBook` не привязана к жизненному циклу соединения
`resyncOrderBook` (`orderbook.go:326-358`) берёт **собственный** `context.WithTimeout(context.Background(), RequestTimeout)` (`orderbook.go:346`) — независимый ни от `ctx` бота, ни от `c.done`, ни от `wsClient.Close()`. Реконнект (`main.go:160/176`, `wsClient.Close()`) закрывает только TCP-соединение, но никак не отменяет и не дожидается (`sync.WaitGroup` отсутствует) эту горутину.

Значит: если разрыв соединения происходит **пока `resyncOrderBook` ещё ждёт ответ REST** (до 10с, `constants.go:11`), эта горутина из "старого поколения" соединения продолжает жить и завершится независимо от нового цикла реконнекта.

**Уверенность:** высокая. **Влияние:** горутина не течёт бесконечно (ограничена таймаутом), но создаёт state-race между поколениями — см. ниже.

### Находка 1.4 — гонка между "старой" resync-горутиной и "новым" `InitOrderBookSnapshots`
Обе точки пишут в `c.books[symbol]` под одним и тем же `booksMu`, но **порядок** между ними не гарантирован:
- Новое поколение: `InitOrderBookSnapshots` → `orderbook.go:309`.
- Старое поколение (переживший реконнект `resyncOrderBook`): `orderbook.go:355`.

Если старая горутина завершается **после** свежей инициализации нового соединения, она молча перезатирает только что созданный (актуальный для нового WS-потока) `LocalOrderBook` ещё одним REST-снапшотом, сбрасывая `synced=false` заново — новому потоку дельт снова нужно искать точку стыковки, лишняя задержка/потеря дельт в самом начале нового сеанса, плюс лишний REST-запрос.

**Уверенность:** средняя-высокая (логически неизбежно при данном коде, конкретная частота зависит от таймингов сети). **Влияние:** среднее — самоизлечивается, но искажает свежесть данных сразу после реконнекта.

### Находка 1.5 — "застревание" resync на новом соединении из-за унаследованного флага
Пока унаследованный `c.resyncing[symbol]=true` не сброшен (сбрасывается только `defer` внутри самой старой горутины, `orderbook.go:327-331`, т.е. до её завершения — до `RequestTimeout`), любой `handleOrderBook` **нового** соединения, обнаруживший разрыв последовательности для того же символа, получит `false` от `tryStartResync` (`parser.go:125-129`) и просто `return` — **новый resync не запустится вообще**, хотя он относится к совсем другому WS-сеансу. Локальный стакан для этого символа может простаивать неопубликованным до ~10 секунд, пока не освободится флаг от старого поколения.

**Уверенность:** высокая по механизму, средняя по частоте проявления в реальности. **Влияние:** потенциальная задержка публикации стакана по конкретному символу сразу после реконнекта — некритично для одного разрыва, но ухудшает свежесть данных именно в момент, когда наблюдаемость важнее всего (нестабильная сеть = частые реконнекты = частые попадания в это окно).

**Корень проблемы для Q1:** отсутствует понятие "поколения соединения" — ни явная отмена fire-and-forget горутин при реконнекте, ни сброс `resyncing`. Комментарий в коде (`connection.go:59-67`) объясняет, зачем нужен флаг `resyncing` вообще, но не рассматривает случай реконнекта во время resync.

---

## Вопрос 2: Ping/Pong vs ReadLoop

### Находка 2.1 — гонка на поле `pingTs` (данные)
- Запись: `pingloop.go:32` (`c.pingTs = time.Now().UnixMilli()`) — выполняется в горутине `RunPingLoop` (`main.go:156`).
- Чтение: `ws.go:58` (`latencyMs := time.Now().UnixMilli() - c.pingTs`) — выполняется в горутине `ReadLoop` (`main.go:155`).

Никакой мьютекс/atomic это поле не защищает: `writeMu` (`connection.go:30`) охраняет только `conn.Write*` (`connection.go:143-147, 153-157`), а не `pingTs`. Комментарий у поля (`connection.go:34`: "используется в pingloop.go") вводит в заблуждение — реально читается из `ws.go`, т.е. из другой горутины.

**Уверенность:** высокая — это классическая гонка, `go test -race` обязана её обнаружить. **Влияние:** на amd64/arm64 разрыва значения не будет (естественно выровненный int64), но нет happens-before — компилятор/процессор формально не обязаны отдавать читателю актуальное значение; на 32-битных платформах чтение/запись int64 в принципе не атомарны без `sync/atomic`. Практический эффект — потенциально неверный расчёт RTT/EMA латентности, не критично для торговой логики напрямую, но загрязняет метрику `EXCH` в TUI.

Замечание для точности: `emaLat` (`connection.go:35`) гонки не имеет — пишется и читается только внутри `ReadLoop` (`ws.go:59` и `ws.go:62`), другой горутиной не трогается.

### Находка 2.2 — гонка на канале `done` (гораздо серьёзнее)
`done` — буферизованный канал ёмкостью 1 (`connection.go:32`, создаётся `connection.go:114` и пересоздаётся `connection.go:131`). Всего один producer, шлёт **не более одного значения за сеанс**:
- `ws.go:29-34` (`signalDone`), вызывается один раз из `ReadLoop` при ошибке чтения (`ws.go:44`).

Но **два независимых consumer'а** делают `select` на этом же канале:
- `pingloop.go:63` — `case <-c.done:` внутри `RunPingLoop`.
- `main.go:174` — `case <-wsClient.Done():` внутри цикла реконнекта.

Единственное отправленное значение получит **только один** из этих `select`-ов (какой именно — не специфицировано языком, зависит от планировщика). Отсюда два сценария:

**А. `RunPingLoop` "выигрывает" гонку** → `main.go:174` никогда не разблокируется через `wsClient.Done()`. Цикл реконнекта в `main.go` зависает навсегда (выйти можно только через `ctx.Done()`, т.е. SIGTERM). Бот молча перестаёт получать/публиковать любые WS-данные без единого реконнекта — требуется ручной рестарт процесса.
**Уверенность:** высокая (прямой вывод из семантики каналов Go). **Влияние:** критическое — полная тишина по данным без видимого краша.

**Б. `main` "выигрывает" гонку** → `RunPingLoop` не получает сигнал через `done` и не завершается по этой ветке. Единственный оставшийся способ для неё завершиться — ошибка при следующем `sendPing()` (`pingloop.go:66-69`). Но `writeJSON` (`connection.go:143-147`) читает **живое** поле `c.conn`, а не захваченное один раз значение — а `Connect()` при реконнекте (`connection.go:182`, `c.conn = conn`) может успеть присвоить **новое** соединение раньше следующего тика старого `RunPingLoop`. В этом случае "осиротевшая" горутина `RunPingLoop` от старого поколения соединения успешно пишет пинги в **новое** соединение — получаем **две параллельно живущие `RunPingLoop`** (старую и новую) на одном и том же `c.conn`, обе пишут `pingTs` (усугубляет находку 2.1) и обе публикуют метрики. Поскольку общий `ctx` у бота один на весь процесс (не пересоздаётся на реконнект), у "осиротевшей" горутины больше никогда не будет шанса завершиться корректно — это утечка горутины, которая может накапливаться при каждом проигранном по этому сценарию реконнекте.
**Уверенность:** высокая по механизму (весь путь прослеживается по коду без предположений), степень практического проявления зависит от совпадения по времени between тиком пинга и скоростью реконнекта. **Влияние:** среднее-высокое — утечка горутин + искажённые замеры RTT, накапливается со временем при нестабильном соединении.

**Вывод по Q2:** гонка между ping/pong и ReadLoop не просто есть — она **не защищена вообще**, и главная проблема не столько `pingTs` (некритичная гонка данных), сколько архитектурная ошибка: канал `done` спроектирован как single-value сигнал, но используется как multicast с двумя получателями. Корректным решением на уровне "что должно быть" было бы `close(c.done)` вместо `c.done <- struct{}{}` (закрытие канала будит **всех** ожидающих получателей), либо отдельный канал/флаг на каждого потребителя — но это уже предложение по фиксу, а не часть аудита поведения текущего кода.

---

