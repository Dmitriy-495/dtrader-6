package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/hub"
	"github.com/redis/go-redis/v9"
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
		go r.pollIndicators(ctx, symbol)
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

// IndicatorsMsg — объединённый снапшот T/V/P от analyzer для одного
// символа, публикуется в новом канале "indicators". Analyzer пишет их
// как 7 раздельных ключей в Redis (indicators:trend:{tf}:{symbol} на
// каждый из 1m/8m/24m, indicators:volume:{tf}:{symbol} аналогично,
// indicators:pressure:{symbol} без {tf} — раздел 6 CHECKPOINT.md).
// ws-server объединяет их в ОДНО сообщение на символ — TUI получает
// цельный, согласованный снапшот за один тик, а не 7 разрозненных
// частичных обновлений, которые пришлось бы склеивать на клиенте.
type IndicatorsMsg struct {
	Trend    map[string]json.RawMessage `json:"trend"`    // ключ — таймфрейм ("1m"/"8m"/"24m")
	Volume   map[string]json.RawMessage `json:"volume"`   // ключ — таймфрейм
	Pressure json.RawMessage            `json:"pressure"` // без таймфрейма
}

// indicatorTimeframes — таймфреймы, на которых analyzer считает T/V.
// Захардкожено здесь же, а не читается из конфига analyzer — ws-server
// не должен зависеть от config.yaml другого сервиса; если состав ТФ
// изменится в analyzer, эту константу нужно будет обновить вручную
// (в паре мест кода, не автоматически — осознанный компромисс простоты
// для v1, см. раздел 13a CHECKPOINT.md про сами таймфреймы).
var indicatorTimeframes = []string{"1m", "8m", "24m"}

// pollIndicators читает T/V/P (indicators:*) для одного символа и
// отправляет объединённым сообщением, только когда содержимое реально
// изменилось — тот же паттерн, что pollStats/pollCandles выше.
// Интервал 5s синхронизирован с calc_interval analyzer по умолчанию
// (config.yaml analyzer) — опрашивать чаще бессмысленно, значения
// физически не обновятся раньше следующего расчётного тика analyzer.
func (r *Reader) pollIndicators(ctx context.Context, symbol string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var lastRaw string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msg := IndicatorsMsg{
				Trend:  make(map[string]json.RawMessage, len(indicatorTimeframes)),
				Volume: make(map[string]json.RawMessage, len(indicatorTimeframes)),
			}
			anyFound := false

			for _, tf := range indicatorTimeframes {
				if val, err := r.rdb.Get(ctx, fmt.Sprintf("indicators:trend:%s:%s", tf, symbol)).Result(); err == nil {
					msg.Trend[tf] = json.RawMessage(val)
					anyFound = true
				}
				if val, err := r.rdb.Get(ctx, fmt.Sprintf("indicators:volume:%s:%s", tf, symbol)).Result(); err == nil {
					msg.Volume[tf] = json.RawMessage(val)
					anyFound = true
				}
			}
			if val, err := r.rdb.Get(ctx, fmt.Sprintf("indicators:pressure:%s", symbol)).Result(); err == nil {
				msg.Pressure = json.RawMessage(val)
				anyFound = true
			}

			if !anyFound {
				// analyzer ещё не публиковал ничего для этого символа
				// (например, сервис только что запустился) — не шлём
				// пустое сообщение клиентам.
				continue
			}

			// Сравниваем по сериализованному виду — тот же приём, что
			// в pollStats/pollCandles: дешевле, чем поле-за-полем, и
			// достаточно надёжно, потому что analyzer сам публикует
			// с TTL и новым ts на каждый расчётный тик — реально
			// идентичный JSON означает, что READER просто попал на
			// тот же самый, ещё не обновившийся снапшот в Redis.
			raw, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			if string(raw) == lastRaw {
				continue
			}
			lastRaw = string(raw)

			r.hub.Broadcast(hub.Message{
				Channel: "indicators",
				Symbol:  symbol,
				Data:    msg,
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
	ServerTs     int64        `json:"server_ts"`     // timestamp ws-server (для SERV latency)
	ExchangePing ExchangePing `json:"exchange_ping"` // латентность биржи current + EMA
	Balance      Balance      `json:"balance"`       // текущий баланс аккаунта
}

// RunSystem запускает горутину heartbeat — шлёт system сообщение каждые 20s.
// Также отправляет сразу при старте не дожидаясь первого тика.
func (r *Reader) RunSystem(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		log.Println("📡 Reader system: heartbeat запущен (интервал 10s)")

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
			ServerTs:     time.Now().UnixMilli(),
			ExchangePing: exchPing,
			Balance:      balance,
		},
	})
}
