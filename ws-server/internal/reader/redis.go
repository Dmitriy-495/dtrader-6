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

type Reader struct {
	rdb     *redis.Client
	hub     *hub.Hub
	symbols []string
}

func New(rdb *redis.Client, h *hub.Hub, symbols []string) *Reader {
	return &Reader{rdb: rdb, hub: h, symbols: symbols}
}

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
	BuyVol    float64 `json:"buy_vol"`    // суммарный объём покупок
	SellVol   float64 `json:"sell_vol"`   // суммарный объём продаж
	BuyCount  int     `json:"buy_count"`  // количество покупок
	SellCount int     `json:"sell_count"` // количество продаж
	LastPrice string  `json:"last_price"` // последняя цена
	Ts        int64   `json:"ts"`         // timestamp агрегата
}

// readTrades читает трейды из Stream и агрегирует за 500ms.
// Клиент получает не каждый тик а сводку каждые полсекунды.
func (r *Reader) readTrades(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:trades:%s", symbol)
	lastID := "$"

	// mu защищает агрегат от одновременного чтения/записи
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
				// сбрасываем агрегат
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

	// Основной цикл: читаем тики из Redis Stream и накапливаем в агрегат
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
					agg.SellVol += -size // храним как положительное
					agg.SellCount++
				}
				agg.LastPrice = price
				mu.Unlock()
			}
		}
	}
}

// readLiquidations читает ликвидации — отправляем каждую без агрегации
// (ликвидации редкие и важные — не стоит их задерживать)
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

// pollOrderBook читает стакан раз в 1s — достаточно для TUI.
// Стакан меняется каждые 100ms но глаз человека не видит разницу быстрее 1s.
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

// pollStats читает статистику и отправляет только при изменении OI или LSR.
// Нет смысла слать одинаковые данные каждые 5 секунд.
func (r *Reader) pollStats(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:stats:%s", symbol)
	ticker := time.NewTicker(20 * time.Second)
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
			// отправляем только если данные изменились
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

// pollCandles читает список свечей и отправляет только когда появляется новая.
// Сравниваем timestamp последней свечи.
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
			// берём только последнюю свечу (индекс 0 = самая новая)
			vals, err := r.rdb.LRange(ctx, key, 0, 0).Result()
			if err != nil || len(vals) == 0 {
				continue
			}
			// отправляем только если свеча новая
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

// SystemMsg — служебное сообщение heartbeat от ws-server к TUI
type SystemMsg struct {
	ServerTs   int64 `json:"server_ts"`   // timestamp ws-server (для SERV latency)
	ExchangeTs int64 `json:"exchange_ts"` // timestamp последнего pong от биржи
}

// RunSystem запускает горутину heartbeat — шлёт system сообщение каждые 5s.
// TUI использует server_ts для расчёта SERV latency,
// exchange_ts для отображения свежести EXCH индикатора.
func (r *Reader) RunSystem(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		log.Println("📡 Reader system: heartbeat запущен (интервал 20s)")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Читаем последний pong от биржи
				var exchTs int64
				val, err := r.rdb.Get(ctx, "system:exchange_ping").Result()
				if err == nil {
					exchTs, _ = strconv.ParseInt(val, 10, 64)
				}

				r.hub.Broadcast(hub.Message{
					Channel: "system",
					Symbol:  "",
					Data: SystemMsg{
						ServerTs:   time.Now().UnixMilli(),
						ExchangeTs: exchTs,
					},
				})
			}
		}
	}()
}
