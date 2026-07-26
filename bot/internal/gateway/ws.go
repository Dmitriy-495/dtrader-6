// Этот файл отвечает за главный цикл чтения сообщений от Gate.io.
// Управление соединением — в connection.go, ping/pong и EMA — в pingloop.go,
// структуры протокола — в protocol.go, подписки на каналы — в subscribe.go,
// разбор конкретных типов рыночных данных — в parser.go.
//
// ReadLoop специально сделан "тонким": он только читает байты, распаковывает
// конверт (WSResponse) и решает, КОМУ отдать Result — сам не занимается
// разбором Trade/Candle/OrderBook и т.д., это уже дело parser.go.
package gateway

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"time"

	"github.com/gorilla/websocket"
)

// ReadLoop — главный цикл чтения сообщений от Gate.io WebSocket.
// Должен запускаться в отдельной горутине (go wsClient.ReadLoop(ctx))
// параллельно с RunPingLoop.
//
// Цикл работает, пока conn.ReadMessage() не вернёт ошибку — это
// происходит либо при отмене ctx (плановое завершение), либо при
// разрыве соединения (сеть, биржа закрыла коннект и т.д.).
func (c *WSClient) ReadLoop(ctx context.Context) {
	signalDone := func() {
		select {
		case c.done <- struct{}{}:
		default:
		}
	}
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("❌ WS ошибка: %v", err)
			}
			signalDone()
			return
		}

		var msg WSResponse
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("⚠️ Не удалось разобрать: %s", string(raw))
			continue
		}

		// --- Служебные случаи (не рыночные данные) ---

		if msg.Channel == "futures.pong" {
			// Считаем RTT и обновляем EMA (updateEMA — см. pingloop.go)
			latencyMs := time.Now().UnixMilli() - c.pingTs
			c.updateEMA(latencyMs)
			// Пишем текущую латентность и EMA в Redis
			if c.pub != nil {
				emaMs := int64(math.Round(c.emaLat))
				if err := c.pub.PublishExchangePing(ctx, latencyMs, emaMs); err != nil {
					log.Printf("⚠️ publish exchange_ping failed: err=%v", err)
					c.pub.Metrics.IncDropped()
				}
			}
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

		// --- Рыночные данные: отдаём в parser.go по каналу ---

		switch msg.Channel {
		case "futures.trades":
			c.handleTrades(ctx, msg.Result)
		case "futures.order_book_update":
			c.handleOrderBook(ctx, msg.Result)
		case "futures.candlesticks":
			c.handleCandles(ctx, msg.Result)
		case "futures.public_liquidates":
			c.handleLiquidations(ctx, msg.Result)
		case "futures.contract_stats":
			c.handleContractStats(ctx, msg.Result)
		}
	}
}
