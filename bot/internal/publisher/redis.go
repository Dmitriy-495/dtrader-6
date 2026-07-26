package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Publisher — единственная точка записи рыночных данных в Redis.
//
// maxTrades/maxLiquidations/maxCandles — лимиты скользящих окон хранения
// (сколько последних записей держим в Redis на символ), приходят из
// config.yaml (секция storage) через New() — раньше были захардкожены
// константами прямо в этом файле, теперь их можно менять без пересборки.
//
// Metrics — публичное поле (не приватное!), потому что parser.go
// (пакет gateway) должен уметь вызывать pub.Metrics.IncDropped()
// напрямую при неудачной публикации, без лишней обёртки-метода
// в самом Publisher.
type Publisher struct {
	rdb     *redis.Client
	Metrics *Metrics

	maxTrades       int64
	maxLiquidations int64
	maxCandles      int64
}

// New создаёт Publisher с новым, обнулённым счётчиком метрик.
//
// maxTrades/maxLiquidations/maxCandles — лимиты хранения из
// config.yaml (storage.trades, storage.liquidations, storage.candles_1m).
// Config.validate() уже гарантирует, что все три положительны — здесь
// дополнительных проверок не делаем.
func New(host string, port int, password string, maxTrades, maxLiquidations, maxCandles int) *Publisher {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       0,
	})
	return &Publisher{
		rdb:             rdb,
		Metrics:         NewMetrics(),
		maxTrades:       int64(maxTrades),
		maxLiquidations: int64(maxLiquidations),
		maxCandles:      int64(maxCandles),
	}
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
		MaxLen: p.maxTrades,
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
	pipe.LTrim(ctx, key, -p.maxCandles, -1)
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
		MaxLen: p.maxLiquidations,
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

// PublishExchangePing записывает текущую латентность ping-pong и EMA в Redis.
// current — текущий RTT в ms, emaMs — экспоненциальная скользящая средняя.
// TTL 60 секунд: если бот упадёт, значение само "протухнет" в Redis
// через минуту — ws-server и TUI увидят отсутствие данных, а не
// устаревшее "последнее известное" значение, выданное за живое.
func (p *Publisher) PublishExchangePing(ctx context.Context, current, emaMs int64) error {
	data := map[string]int64{"current": current, "ema": emaMs}
	raw, _ := json.Marshal(data)
	return p.rdb.Set(ctx, "system:exchange_ping", raw, 60*time.Second).Err()
}

// PublishMetrics записывает текущее значение счётчика пропущенных
// публикаций в Redis. Вызывается из RunPingLoop раз в 10 секунд —
// тем же ритмом, что и PublishExchangePing, чтобы не создавать лишнюю
// нагрузку на Redis отдельным циклом.
//
// TTL тот же принцип, что у exchange_ping: если бот упал, значение
// протухнет через минуту, а не будет висеть в Redis как будто бот жив.
func (p *Publisher) PublishMetrics(ctx context.Context) error {
	data := map[string]int64{"dropped_publications": p.Metrics.Dropped()}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("PublishMetrics marshal: %w", err)
	}
	return p.rdb.Set(ctx, "system:bot_metrics", raw, 60*time.Second).Err()
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
