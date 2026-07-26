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

	"github.com/gorilla/websocket"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/publisher"
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
	done    chan struct{} // закрывается/сигналит, когда соединение разорвано

	pingTs int64   // timestamp последнего отправленного ping (unix ms) — используется в pingloop.go
	emaLat float64 // EMA латентности (ms) — используется в pingloop.go
}

// NewWSClient создаёт новый WS-клиент. Соединение ещё не устанавливается —
// для этого нужно отдельно вызвать Connect.
func NewWSClient(url, apiKey, secret string, pub *publisher.Publisher) *WSClient {
	return &WSClient{
		url:    url,
		apiKey: apiKey,
		secret: secret,
		pub:    pub,
		done:   make(chan struct{}, 1),
	}
}

// Done возвращает канал, который сигналит о разрыве соединения.
// main.go слушает этот канал в select, чтобы понять "пора реконнектиться".
func (c *WSClient) Done() <-chan struct{} {
	return c.done
}

// ResetDone создаёт новый канал done перед каждой новой попыткой подключения.
// Нужно, потому что закрытый (или уже просигналивший) канал нельзя
// переиспользовать — реконнект в main.go начинается с чистого канала.
func (c *WSClient) ResetDone() {
	c.done = make(chan struct{}, 1)
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
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, header)
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
