package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

type WSRequest struct {
	Time    int64    `json:"time"`
	Channel string   `json:"channel"`
	Event   string   `json:"event,omitempty"`
	Payload []string `json:"payload,omitempty"`
}

type WSResponse struct {
	Time    int64           `json:"time"`
	Channel string          `json:"channel"`
	Event   string          `json:"event,omitempty"`
	Error   *WSError        `json:"error,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

type WSError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type WSClient struct {
	url     string
	apiKey  string
	secret  string
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func NewWSClient(url, apiKey, secret string) *WSClient {
	return &WSClient{url: url, apiKey: apiKey, secret: secret}
}

func (c *WSClient) writeJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}

func (c *WSClient) writeMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

func (c *WSClient) Connect(ctx context.Context) error {
	header := http.Header{
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

func (c *WSClient) sendPing() error {
	return c.writeJSON(WSRequest{
		Time:    utils.NowUnix(),
		Channel: "futures.ping",
	})
}

func (c *WSClient) RunPingLoop(ctx context.Context) {
	if err := c.sendPing(); err != nil {
		log.Printf("❌ Первый ping не удался: %v", err)
		return
	}
	log.Printf("🏓 Первый ping отправлен [%d]", utils.NowUnix())

	// Увеличили с 10 до 20 секунд — проверяем гипотезу о throttle биржи
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	log.Println("🏓 Ping loop запущен (интервал 20s)")

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Ping loop остановлен")
			return
		case <-ticker.C:
			if err := c.sendPing(); err != nil {
				log.Printf("❌ Ошибка ping: %v", err)
				return
			}
			log.Printf("🏓 Ping отправлен [%d]", utils.NowUnix())
		}
	}
}

func (c *WSClient) ReadLoop(ctx context.Context) {
	log.Println("👂 Read loop запущен")

	var tickerCount int

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				log.Println("🛑 Read loop остановлен")
				return
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Println("🔌 WS закрыт биржей штатно")
				return
			}
			log.Printf("❌ WS ошибка: %v", err)
			return
		}

		var msg WSResponse
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("⚠️ Не удалось разобрать: %s", string(raw))
			continue
		}

		if msg.Channel == "futures.pong" {
			log.Printf("🏓 Pong получен [%d]", msg.Time)
			continue
		}

		if msg.Error != nil {
			log.Printf("❌ Ошибка биржи: code=%d msg=%s channel=%s",
				msg.Error.Code, msg.Error.Message, msg.Channel)
			continue
		}

		if msg.Event == "subscribe" {
			log.Printf("✅ Подписка подтверждена: channel=%s", msg.Channel)
			continue
		}

		switch msg.Channel {
		case "futures.tickers":
			tickerCount++
			preview := string(raw)
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			log.Printf("📈 [%d] %s", tickerCount, preview)
		}
	}
}

func (c *WSClient) Close() {
	if c.conn != nil {
		c.writeMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.conn.Close()
		log.Println("🔌 WS соединение закрыто")
	}
}
