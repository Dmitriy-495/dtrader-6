package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	maxTrades       = 1000
	maxLiquidations = 500
	maxCandles      = 200
)

type Publisher struct {
	rdb *redis.Client
}

func New(host string, port int, password string) *Publisher {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       0,
	})
	return &Publisher{rdb: rdb}
}

func (p *Publisher) Ping(ctx context.Context) error {
	return p.rdb.Ping(ctx).Err()
}

func (p *Publisher) Close() error {
	return p.rdb.Close()
}

func (p *Publisher) PublishTrade(ctx context.Context, symbol string, data map[string]interface{}) error {
	key := fmt.Sprintf("market:trades:%s", symbol)
	err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: maxTrades,
		Approx: true,
		Values: data,
	}).Err()
	if err != nil {
		return fmt.Errorf("PublishTrade %s: %w", symbol, err)
	}
	return nil
}

func (p *Publisher) PublishOrderBook(ctx context.Context, symbol string, data interface{}) error {
	key := fmt.Sprintf("market:orderbook:%s", symbol)
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("PublishOrderBook marshal %s: %w", symbol, err)
	}
	if err := p.rdb.Set(ctx, key, raw, 0).Err(); err != nil {
		return fmt.Errorf("PublishOrderBook %s: %w", symbol, err)
	}
	return nil
}

func (p *Publisher) PublishCandle(ctx context.Context, symbol string, data interface{}) error {
	key := fmt.Sprintf("market:candles:1m:%s", symbol)
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("PublishCandle marshal %s: %w", symbol, err)
	}
	pipe := p.rdb.Pipeline()
	pipe.RPush(ctx, key, raw)
	pipe.LTrim(ctx, key, -maxCandles, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("PublishCandle %s: %w", symbol, err)
	}
	log.Printf("🕯️ [redis] свеча записана: %s", symbol)
	return nil
}

func (p *Publisher) PublishLiquidation(ctx context.Context, symbol string, data map[string]interface{}) error {
	key := fmt.Sprintf("market:liquidations:%s", symbol)
	err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: maxLiquidations,
		Approx: true,
		Values: data,
	}).Err()
	if err != nil {
		return fmt.Errorf("PublishLiquidation %s: %w", symbol, err)
	}
	return nil
}

func (p *Publisher) PublishContractStats(ctx context.Context, symbol string, data interface{}) error {
	key := fmt.Sprintf("market:stats:%s", symbol)
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("PublishContractStats marshal %s: %w", symbol, err)
	}
	if err := p.rdb.Set(ctx, key, raw, 0).Err(); err != nil {
		return fmt.Errorf("PublishContractStats %s: %w", symbol, err)
	}
	return nil
}

// PublishExchangePing — записывает timestamp последнего pong от биржи.
// ws-server читает это значение и транслирует клиентам как EXCH индикатор.
func (p *Publisher) PublishExchangePing(ctx context.Context) error {
	return p.rdb.Set(ctx, "system:exchange_ping", time.Now().UnixMilli(), 60*time.Second).Err()
}

// PublishBalance — записывает баланс аккаунта в Redis при старте бота.
func (p *Publisher) PublishBalance(ctx context.Context, total, margin, leverage string) error {
	data := map[string]string{
		"total":    total,
		"margin":   margin,
		"leverage": leverage,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return p.rdb.Set(ctx, "account:balance", raw, 0).Err()
}
