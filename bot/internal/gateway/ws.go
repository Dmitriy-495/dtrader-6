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
	"github.com/Dmitriy-495/dtrader-6/bot/internal/publisher"
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

// WSClient — WebSocket клиент биржи.
// pub — издатель данных в Redis, nil в режиме разработки без Redis.
type WSClient struct {
	url     string
	apiKey  string
	secret  string
	conn    *websocket.Conn
	writeMu sync.Mutex
	pub     *publisher.Publisher
}

func NewWSClient(url, apiKey, secret string, pub *publisher.Publisher) *WSClient {
	return &WSClient{url: url, apiKey: apiKey, secret: secret, pub: pub}
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

// --- Структуры для парсинга входящих сообщений ---

// Trade — одна сделка из futures.trades
type Trade struct {
	ID         int64   `json:"id"`
	Contract   string  `json:"contract"`
	Size       string  `json:"size"`
	Price      string  `json:"price"`
	CreateTime int64   `json:"create_time"`
	CreateTimeMs int64 `json:"create_time_ms"`
	IsInternal bool    `json:"is_internal"`
}

// OBLevel — уровень стакана {p: price, s: size}
type OBLevel struct {
	Price string `json:"p"`
	Size  string `json:"s"`
}

// OrderBookUpdate — обновление стакана из futures.order_book_update
type OrderBookUpdate struct {
	T        int64     `json:"t"`
	S        string    `json:"s"`
	U        int64     `json:"u"`
	FirstU   int64     `json:"U"`
	Bids     []OBLevel `json:"b"`
	Asks     []OBLevel `json:"a"`
}

// Candle — свеча из futures.candlesticks
type Candle struct {
	T      int64  `json:"t"`
	Open   string `json:"o"`
	Close  string `json:"c"`
	High   string `json:"h"`
	Low    string `json:"l"`
	Volume string `json:"v"`
	Name   string `json:"n"`
	Amount string `json:"a"`
	Window bool   `json:"w"` // true = свеча закрыта
}

// Liquidation — ликвидация из futures.public_liquidates
type Liquidation struct {
	Price    float64 `json:"price"`
	Size     string  `json:"size"`
	TimeMs   int64   `json:"time_ms"`
	Contract string  `json:"contract"`
}

// ContractStats — статистика контракта из futures.contract_stats
type ContractStats struct {
	Time            int64   `json:"time"`
	Contract        string  `json:"contract"`
	OpenInterest    string  `json:"open_interest"`
	OpenInterestUSD float64 `json:"open_interest_usd"`
	LsrTaker        float64 `json:"lsr_taker"`
	LsrAccount      float64 `json:"lsr_account"`
	LongLiqSize     string  `json:"long_liq_size"`
	ShortLiqSize    string  `json:"short_liq_size"`
	LongLiqUSD      float64 `json:"long_liq_usd"`
	ShortLiqUSD     float64 `json:"short_liq_usd"`
	TopLsrAccount   string  `json:"top_lsr_account"`
	TopLsrSize      string  `json:"top_lsr_size"`
	MarkPrice       float64 `json:"mark_price"`
}

func (c *WSClient) ReadLoop(ctx context.Context) {
	log.Println("👂 Read loop запущен")

	var (
		tradeCount int
		obCount    int
		candleCount int
		liqCount   int
		statsCount int
	)

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

		case "futures.trades":
			var trades []Trade
			if err := json.Unmarshal(msg.Result, &trades); err != nil {
				log.Printf("⚠️ trades parse error: %v", err)
				continue
			}
			for _, t := range trades {
				// пропускаем внутренние трейды — не попадают в свечи
				if t.IsInternal {
					continue
				}
				tradeCount++
				if c.pub != nil {
					_ = c.pub.PublishTrade(ctx, t.Contract, map[string]interface{}{
						"id":    t.ID,
						"price": t.Price,
						"size":  t.Size,
						"ts":    t.CreateTimeMs,
					})
				}
			}
			if tradeCount%20 == 0 {
				log.Printf("💹 [%d] trades записано в Redis", tradeCount)
			}

		case "futures.order_book_update":
			var ob OrderBookUpdate
			if err := json.Unmarshal(msg.Result, &ob); err != nil {
				log.Printf("⚠️ order_book_update parse error: %v", err)
				continue
			}
			obCount++
			if c.pub != nil {
				_ = c.pub.PublishOrderBook(ctx, ob.S, ob)
			}
			if obCount%100 == 0 {
				log.Printf("📖 [%d] orderbook записан в Redis", obCount)
			}

		case "futures.candlesticks":
			var candles []Candle
			if err := json.Unmarshal(msg.Result, &candles); err != nil {
				log.Printf("⚠️ candlesticks parse error: %v", err)
				continue
			}
			for _, candle := range candles {
				candleCount++
				// записываем только закрытую свечу (w=true)
				if candle.Window && c.pub != nil {
					// извлекаем символ из имени "1m_BTC_USDT" → "BTC_USDT"
					symbol := candle.Name
					if len(symbol) > 3 {
						symbol = symbol[3:] // убираем "1m_"
					}
					_ = c.pub.PublishCandle(ctx, symbol, candle)
				}
			}
			if candleCount%10 == 0 {
				log.Printf("🕯️ [%d] свечей обработано", candleCount)
			}

		case "futures.public_liquidates":
			var liqs []Liquidation
			if err := json.Unmarshal(msg.Result, &liqs); err != nil {
				log.Printf("⚠️ liquidates parse error: %v", err)
				continue
			}
			for _, liq := range liqs {
				liqCount++
				if c.pub != nil {
					_ = c.pub.PublishLiquidation(ctx, liq.Contract, map[string]interface{}{
						"price":   liq.Price,
						"size":    liq.Size,
						"time_ms": liq.TimeMs,
					})
				}
				log.Printf("💥 [%d] ликвидация: %s price=%.1f size=%s",
					liqCount, liq.Contract, liq.Price, liq.Size)
			}

		case "futures.contract_stats":
			var stats ContractStats
			if err := json.Unmarshal(msg.Result, &stats); err != nil {
				log.Printf("⚠️ contract_stats parse error: %v", err)
				continue
			}
			statsCount++
			if c.pub != nil {
				_ = c.pub.PublishContractStats(ctx, stats.Contract, stats)
			}
			log.Printf("📊 [%d] stats: %s OI=%s LSR=%.3f",
				statsCount, stats.Contract, stats.OpenInterest, stats.LsrTaker)
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
