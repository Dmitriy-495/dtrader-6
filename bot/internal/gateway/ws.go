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

type WSClient struct {
	url     string
	apiKey  string
	secret  string
	conn    *websocket.Conn
	writeMu sync.Mutex
	pub     *publisher.Publisher
	done    chan struct{}
}

func NewWSClient(url, apiKey, secret string, pub *publisher.Publisher) *WSClient {
	return &WSClient{
		url:    url,
		apiKey: apiKey,
		secret: secret,
		pub:    pub,
		done:   make(chan struct{}, 1),
	}
}

func (c *WSClient) Done() <-chan struct{} {
	return c.done
}

func (c *WSClient) ResetDone() {
	c.done = make(chan struct{}, 1)
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
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.sendPing(); err != nil {
				log.Printf("❌ Ошибка ping: %v", err)
				return
			}
		}
	}
}

type Trade struct {
	ID           int64  `json:"id"`
	Contract     string `json:"contract"`
	Size         string `json:"size"`
	Price        string `json:"price"`
	CreateTime   int64  `json:"create_time"`
	CreateTimeMs int64  `json:"create_time_ms"`
	IsInternal   bool   `json:"is_internal"`
}

type OBLevel struct {
	Price string `json:"p"`
	Size  string `json:"s"`
}

type OrderBookUpdate struct {
	T      int64     `json:"t"`
	S      string    `json:"s"`
	U      int64     `json:"u"`
	FirstU int64     `json:"U"`
	Bids   []OBLevel `json:"b"`
	Asks   []OBLevel `json:"a"`
}

type Candle struct {
	T      int64  `json:"t"`
	Open   string `json:"o"`
	Close  string `json:"c"`
	High   string `json:"h"`
	Low    string `json:"l"`
	Volume string `json:"v"`
	Name   string `json:"n"`
	Amount string `json:"a"`
	Window bool   `json:"w"`
}

type Liquidation struct {
	Price    float64 `json:"price"`
	Size     string  `json:"size"`
	TimeMs   int64   `json:"time_ms"`
	Contract string  `json:"contract"`
}

type ContractStats struct {
	Time            int64       `json:"time"`
	Contract        string      `json:"contract"`
	OpenInterest    json.Number `json:"open_interest"`
	OpenInterestUSD json.Number `json:"open_interest_usd"`
	LsrTaker        json.Number `json:"lsr_taker"`
	LsrAccount      json.Number `json:"lsr_account"`
	LongLiqSize     json.Number `json:"long_liq_size"`
	ShortLiqSize    json.Number `json:"short_liq_size"`
	LongLiqUSD      json.Number `json:"long_liq_usd"`
	ShortLiqUSD     json.Number `json:"short_liq_usd"`
	TopLsrAccount   json.Number `json:"top_lsr_account"`
	TopLsrSize      json.Number `json:"top_lsr_size"`
	MarkPrice       json.Number `json:"mark_price"`
}

// parseLiquidations парсит ликвидации — биржа шлёт то массив то объект
func parseLiquidations(raw json.RawMessage) ([]Liquidation, error) {
	// пробуем массив
	var liqs []Liquidation
	if err := json.Unmarshal(raw, &liqs); err == nil {
		return liqs, nil
	}
	// пробуем одиночный объект
	var liq Liquidation
	if err := json.Unmarshal(raw, &liq); err == nil {
		return []Liquidation{liq}, nil
	}
	return nil, fmt.Errorf("не удалось распарсить ликвидацию")
}

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
		if msg.Channel == "futures.pong" {
			if c.pub != nil {
				_ = c.pub.PublishExchangePing(ctx)
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
		switch msg.Channel {
		case "futures.trades":
			var trades []Trade
			if err := json.Unmarshal(msg.Result, &trades); err != nil {
				log.Printf("⚠️ trades parse error: %v", err)
				continue
			}
			for _, t := range trades {
				if t.IsInternal {
					continue
				}
				if c.pub != nil {
					_ = c.pub.PublishTrade(ctx, t.Contract, map[string]interface{}{
						"id":    t.ID,
						"price": t.Price,
						"size":  t.Size,
						"ts":    t.CreateTimeMs,
					})
				}
			}
		case "futures.order_book_update":
			var ob OrderBookUpdate
			if err := json.Unmarshal(msg.Result, &ob); err != nil {
				log.Printf("⚠️ order_book_update parse error: %v", err)
				continue
			}
			if c.pub != nil {
				_ = c.pub.PublishOrderBook(ctx, ob.S, ob)
			}
		case "futures.candlesticks":
			var candles []Candle
			if err := json.Unmarshal(msg.Result, &candles); err != nil {
				log.Printf("⚠️ candlesticks parse error: %v", err)
				continue
			}
			for _, candle := range candles {
				if candle.Window && c.pub != nil {
					symbol := candle.Name
					if len(symbol) > 3 {
						symbol = symbol[3:]
					}
					_ = c.pub.PublishCandle(ctx, symbol, candle)
				}
			}
		case "futures.public_liquidates":
			liqs, err := parseLiquidations(msg.Result)
			if err != nil {
				log.Printf("⚠️ liquidates parse error: %v", err)
				continue
			}
			for _, liq := range liqs {
				if c.pub != nil {
					_ = c.pub.PublishLiquidation(ctx, liq.Contract, map[string]interface{}{
						"price":   liq.Price,
						"size":    liq.Size,
						"time_ms": liq.TimeMs,
					})
				}
			}
		case "futures.contract_stats":
			var stats ContractStats
			if err := json.Unmarshal(msg.Result, &stats); err != nil {
				log.Printf("⚠️ contract_stats parse error: %v", err)
				continue
			}
			if c.pub != nil {
				_ = c.pub.PublishContractStats(ctx, stats.Contract, stats)
			}
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
	}
}
