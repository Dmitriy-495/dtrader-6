package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

func (r *Reader) readTrades(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:trades:%s", symbol)
	lastID := "$"
	log.Printf("📡 Reader trades: слушаем %s", key)
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
				r.hub.Broadcast(hub.Message{
					Channel: "trades",
					Symbol:  symbol,
					Data:    msg.Values,
				})
			}
		}
	}
}

func (r *Reader) readLiquidations(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:liquidations:%s", symbol)
	lastID := "$"
	log.Printf("📡 Reader liquidations: слушаем %s", key)
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

func (r *Reader) pollOrderBook(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:orderbook:%s", symbol)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	log.Printf("📡 Reader orderbook: опрашиваем %s каждые 200ms", key)
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
				log.Printf("⚠️ Reader orderbook %s parse: %v", symbol, err)
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

func (r *Reader) pollStats(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:stats:%s", symbol)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	log.Printf("📡 Reader stats: опрашиваем %s каждые 5s", key)
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
			var data interface{}
			if err := json.Unmarshal([]byte(val), &data); err != nil {
				log.Printf("⚠️ Reader stats %s parse: %v", symbol, err)
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

func (r *Reader) pollCandles(ctx context.Context, symbol string) {
	key := fmt.Sprintf("market:candles:1m:%s", symbol)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	log.Printf("📡 Reader candles: опрашиваем %s каждые 10s", key)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			vals, err := r.rdb.LRange(ctx, key, 0, 2).Result()
			if err != nil {
				log.Printf("⚠️ Reader candles %s: %v", symbol, err)
				continue
			}
			if len(vals) == 0 {
				continue
			}
			var candles []interface{}
			for _, v := range vals {
				var candle interface{}
				if err := json.Unmarshal([]byte(v), &candle); err == nil {
					candles = append(candles, candle)
				}
			}
			if len(candles) > 0 {
				r.hub.Broadcast(hub.Message{
					Channel: "candles",
					Symbol:  symbol,
					Data:    candles,
				})
			}
		}
	}
}
