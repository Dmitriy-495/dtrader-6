// ws.go — WebSocket соединение с Gate.io.
// Отвечает за: коннект, ping/pong, reconnect.
// Подписки на данные — в subscribe.go (следующий шаг).
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

// WSMessage — универсальная структура сообщения Gate.io WebSocket.
// Gate.io использует один формат для всех сообщений: ping, pong, подписки, данные.
type WSMessage struct {
	// Time — Unix timestamp сообщения
	Time    int64  `json:"time"`
	// Channel — канал: "futures.ping", "futures.pong", "futures.candlesticks" и т.д.
	Channel string `json:"channel"`
	// Event — тип события: "subscribe", "unsubscribe", "update", "all"
	Event   string `json:"event,omitempty"`
	// Error — ошибка от биржи если что-то пошло не так
	Error   *WSError `json:"error,omitempty"`
}

// WSError — структура ошибки от Gate.io WebSocket.
type WSError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// WSClient — WebSocket клиент Gate.io.
type WSClient struct {
	// url — WebSocket URL биржи
	url    string
	// apiKey и secret — для авторизованных подписок (ордера, позиции)
	apiKey string
	secret string
	// conn — активное WS соединение (nil если не подключены)
	conn   *websocket.Conn
}

// NewWSClient создаёт новый WebSocket клиент.
func NewWSClient(url, apiKey, secret string) *WSClient {
	return &WSClient{
		url:    url,
		apiKey: apiKey,
		secret: secret,
	}
}

// Connect устанавливает WebSocket соединение с Gate.io.
func (c *WSClient) Connect(ctx context.Context) error {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("WS коннект не удался: %w", err)
	}
	c.conn = conn
	log.Printf("✅ WS подключён: %s", c.url)
	return nil
}

// sendPing отправляет ping сообщение на Gate.io.
// Gate.io требует ping каждые ~10 секунд иначе закрывает соединение.
func (c *WSClient) sendPing() error {
	msg := WSMessage{
		Time:    utils.NowUnix(),
		Channel: "futures.ping",
	}
	return c.conn.WriteJSON(msg)
}

// RunPingLoop запускает цикл ping/pong — держит соединение живым.
// Блокирующий вызов — запускать в горутине: go client.RunPingLoop(ctx)
//
// Завершается когда:
//   - ctx отменён (штатное завершение)
//   - соединение разорвано (ошибка записи)
func (c *WSClient) RunPingLoop(ctx context.Context) {
	// Тикер срабатывает каждые 10 секунд.
	ticker := time.NewTicker(10 * time.Second)
	// defer Stop() — освобождаем тикер при выходе из функции.
	defer ticker.Stop()

	log.Println("🏓 Ping loop запущен")

	for {
		select {
		// ctx.Done() — контекст отменён, завершаем loop.
		case <-ctx.Done():
			log.Println("🛑 Ping loop остановлен")
			return

		// ticker.C — канал тикера, срабатывает каждые 10 секунд.
		case <-ticker.C:
			if err := c.sendPing(); err != nil {
				log.Printf("❌ Ошибка ping: %v", err)
				return
			}
			log.Printf("🏓 Ping отправлен [%d]", utils.NowUnix())
		}
	}
}

// ReadLoop читает входящие сообщения от Gate.io.
// Блокирующий вызов — запускать в горутине: go client.ReadLoop(ctx)
func (c *WSClient) ReadLoop(ctx context.Context) {
	log.Println("👂 Read loop запущен")

	for {
		// Проверяем контекст перед каждым чтением.
		select {
		case <-ctx.Done():
			log.Println("🛑 Read loop остановлен")
			return
		default:
		}

		// Читаем следующее сообщение от биржи.
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("❌ Ошибка чтения WS: %v", err)
			return
		}

		// Парсим JSON в WSMessage.
		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("⚠️ Не удалось разобрать сообщение: %s", string(raw))
			continue
		}

		// Обрабатываем pong — подтверждение нашего ping.
		if msg.Channel == "futures.pong" {
			log.Printf("🏓 Pong получен [%d]", msg.Time)
			continue
		}

		// Остальные сообщения — логируем пока без обработки.
		// TODO: роутинг по msg.Channel в subscribe.go
		log.Printf("📨 Сообщение: channel=%s event=%s", msg.Channel, msg.Event)
	}
}

// Close закрывает WebSocket соединение.
func (c *WSClient) Close() {
	if c.conn != nil {
		c.conn.Close()
		log.Println("🔌 WS соединение закрыто")
	}
}
