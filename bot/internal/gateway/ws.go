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
	// signalDone закрывает c.done — НЕ отправляет в него значение.
	//
	// У канала done ДВА независимых получателя (main.go:174 в цикле
	// реконнекта и pingloop.go:63 внутри RunPingLoop), оба делают
	// select на одном и том же канале. Отправка одного значения
	// (c.done <- struct{}{}) досталась бы только ОДНОМУ из них —
	// какому именно, не специфицировано языком Go, зависит от
	// планировщика. Раньше здесь так и было сделано, что создавало
	// критическую гонку — найдено независимым аудитом (OpenCode +
	// Claude Sonnet 5, 2026-08-11):
	//   - если "выигрывал" RunPingLoop — цикл реконнекта в main.go
	//     никогда не разблокировался, бот молча замирал без единого
	//     реконнекта, требовался ручной рестарт процесса;
	//   - если "выигрывал" main — RunPingLoop не получал сигнал и
	//     мог продолжать слать пинги в УЖЕ переподключённое соединение
	//     (т.к. читает live-поле c.conn, не захваченную ссылку),
	//     создавая двух параллельно живущих RunPingLoop и утечку
	//     горутины на каждый такой проигранный забег.
	//
	// close() решает это правильно: закрытие канала будит ВСЕХ
	// получателей, ожидающих на нём в select — не одного случайного.
	// recover() на случай двойного close() (защита от паники, если
	// signalDone почему-то будет вызвана дважды за один жизненный
	// цикл соединения — на сегодня по коду такого не происходит, но
	// паника здесь была бы намного хуже гонки, которую мы чиним).
	signalDone := func() {
		defer func() {
			_ = recover()
		}()
		close(c.done)
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
			// Считаем RTT и обновляем EMA (updateEMA — см. pingloop.go).
			// pingTs.Load() — см. комментарий у поля pingTs в connection.go,
			// почему это atomic.Int64, а не обычное поле.
			latencyMs := time.Now().UnixMilli() - c.pingTs.Load()
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
