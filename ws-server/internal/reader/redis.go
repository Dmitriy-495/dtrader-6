package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/hub"
)

// Reader — читает данные из Redis и транслирует клиентам через Hub.
// Каждый канал (trades, orderbook, stats, candles, liquidations) — отдельная горутина.
type Reader struct {
	rdb     *redis.Client
	hub     *hub.Hub
	symbols []string
}

func New(rdb *redis.Client, h *hub.Hub, symbols []string) *Reader {
	return &Reader{rdb: rdb, hub: h, symbols: symbols}
}

// RunAll запускает горутины чтения для всех символов
func (r *Reader) RunAll(ctx context.Context) {
	for _, symbol := range r.symbols {
		go r.readTrades(ctx, symbol)
		go r.readLiquidations(ctx, symbol)
		go r.pollOrderBook(ctx, symbol)
		go r.pollStats(ctx, symbol)
		go r.pollCandles(ctx, symbol)
	}
	log.Printf("📡 Reader: запущены горутины для %d символов", len(r.symbols))
}

// TradeAgg — агрегированные трейды за интервал 500ms.
// Вместо потока тиков клиент получает сводку:
// количество сделок, суммарный объём, направление давления.
type TradeAgg struct {
	Symbol    string  `json:"symbol"`
	BuyVol    float64 `json:"buy_vol"`
	SellVol   float64 `json:"sell_vol"`
	BuyCount  int     `json:"buy_count"`
	SellCount int     `json:"sell_count"`
	LastPrice string  `json:"last_price"`
	Ts        int64   `json:"ts"`
}

// readTrades читает трейды из Stream и агрегирует за 500ms.
func (r *Reader) readTrades(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:trades:%s", symbol)
	lastID := "$"

	var mu sync.Mutex
	agg := &TradeAgg{Symbol: symbol}

	// Горутина-отправщик: каждые 500ms отправляет агрегат если есть данные
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				if agg.BuyCount+agg.SellCount == 0 {
					mu.Unlock()
					continue
				}
				snapshot := *agg
				agg = &TradeAgg{Symbol: symbol}
				mu.Unlock()

				snapshot.Ts = time.Now().UnixMilli()
				r.hub.Broadcast(hub.Message{
					Channel: "trades",
					Symbol:  symbol,
					Data:    snapshot,
				})
			}
		}
	}()

	// Основной цикл: читаем тики из Redis Stream
	for {
		if ctx.Err() != nil {
			return
		}
		streams, err := r.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{key, lastID},
			Count:   100,
			Block:   5 * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("⚠️ Reader trades %s: %v", symbol, err)
			time.Sleep(time.Second)
			continue
		}
		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				size, _ := strconv.ParseFloat(fmt.Sprintf("%v", msg.Values["size"]), 64)
				price := fmt.Sprintf("%v", msg.Values["price"])

				mu.Lock()
				if size > 0 {
					agg.BuyVol += size
					agg.BuyCount++
				} else {
					agg.SellVol += -size
					agg.SellCount++
				}
				agg.LastPrice = price
				mu.Unlock()
			}
		}
	}
}

// readLiquidations — ликвидации редкие и важные, отправляем каждую без агрегации
func (r *Reader) readLiquidations(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:liquidations:%s", symbol)
	lastID := "$"
	for {
		if ctx.Err() != nil {
			return
		}
		streams, err := r.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{key, lastID},
			Count:   50,
			Block:   5 * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("⚠️ Reader liquidations %s: %v", symbol, err)
			time.Sleep(time.Second)
			continue
		}
		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				r.hub.Broadcast(hub.Message{
					Channel: "liquidations",
					Symbol:  symbol,
					Data:    msg.Values,
				})
			}
		}
	}
}

// pollOrderBook читает стакан раз в 1s — достаточно для TUI
func (r *Reader) pollOrderBook(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:orderbook:%s", symbol)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			val, err := r.rdb.Get(ctx, key).Result()
			if err != nil {
				if err != redis.Nil {
					log.Printf("⚠️ Reader orderbook %s: %v", symbol, err)
				}
				continue
			}
			var data interface{}
			if err := json.Unmarshal([]byte(val), &data); err != nil {
				continue
			}
			r.hub.Broadcast(hub.Message{
				Channel: "orderbook",
				Symbol:  symbol,
				Data:    data,
			})
		}
	}
}

// pollStats отправляет только при изменении данных
func (r *Reader) pollStats(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:stats:%s", symbol)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var lastVal string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			val, err := r.rdb.Get(ctx, key).Result()
			if err != nil {
				if err != redis.Nil {
					log.Printf("⚠️ Reader stats %s: %v", symbol, err)
				}
				continue
			}
			if val == lastVal {
				continue
			}
			lastVal = val
			var data interface{}
			if err := json.Unmarshal([]byte(val), &data); err != nil {
				continue
			}
			r.hub.Broadcast(hub.Message{
				Channel: "stats",
				Symbol:  symbol,
				Data:    data,
			})
		}
	}
}

// pollCandles отправляет только когда появляется новая свеча
func (r *Reader) pollCandles(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:candles:1m:%s", symbol)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var lastTs string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			vals, err := r.rdb.LRange(ctx, key, 0, 0).Result()
			if err != nil || len(vals) == 0 {
				continue
			}
			if vals[0] == lastTs {
				continue
			}
			lastTs = vals[0]
			var candle interface{}
			if err := json.Unmarshal([]byte(vals[0]), &candle); err != nil {
				continue
			}
			r.hub.Broadcast(hub.Message{
				Channel: "candles",
				Symbol:  symbol,
				Data:    candle,
			})
		}
	}
}

// Balance — структура баланса аккаунта
type Balance struct {
	Total    string `json:"total"`
	Margin   string `json:"margin"`
	Leverage string `json:"leverage"`
}

// ExchangePing — структура латентности биржи
type ExchangePing struct {
	Current int64 `json:"current"` // текущий RTT в мс
	Ema     int64 `json:"ema"`     // EMA за ~100 измерений
}

// SystemMsg — служебное сообщение heartbeat от ws-server к TUI
type SystemMsg struct {
	ServerTs    int64        `json:"server_ts"`    // timestamp ws-server (для SERV latency)
	ExchangePing ExchangePing `json:"exchange_ping"` // латентность биржи current + EMA
	Balance     Balance      `json:"balance"`       // текущий баланс аккаунта
}

// RunSystem запускает горутину heartbeat — шлёт system сообщение каждые 20s.
// Также отправляет сразу при старте не дожидаясь первого тика.
func (r *Reader) RunSystem(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		log.Println("📡 Reader system: heartbeat запущен (интервал 20s)")

		r.broadcastSystem(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.broadcastSystem(ctx)
			}
		}
	}()
}

// broadcastSystem читает данные из Redis и шлёт system сообщение всем клиентам
func (r *Reader) broadcastSystem(ctx context.Context) {
	// Читаем латентность биржи — JSON {"current": X, "ema": Y}
	var exchPing ExchangePing
	if val, err := r.rdb.Get(ctx, "system:exchange_ping").Result(); err == nil {
		_ = json.Unmarshal([]byte(val), &exchPing)
	}

	// Читаем баланс аккаунта
	var balance Balance
	if val, err := r.rdb.Get(ctx, "account:balance").Result(); err == nil {
		_ = json.Unmarshal([]byte(val), &balance)
	}

	r.hub.Broadcast(hub.Message{
		Channel: "system",
		Symbol:  "",
		Data: SystemMsg{
			ServerTs:    time.Now().UnixMilli(),
			ExchangePing: exchPing,
			Balance:     balance,
		},
	})
}
