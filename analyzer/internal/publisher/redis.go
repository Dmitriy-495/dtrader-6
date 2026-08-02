// Пакет publisher — единственная точка записи analyzer в Redis, по
// прямой аналогии с publisher.Publisher в bot: engine/ никогда не
// вызывает rdb.Set напрямую, только через методы этого пакета — так
// формат ключей и TTL описаны в одном месте, а не разбросаны по всему
// коду engine.
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/indicator"
)

// indicatorTTL — TTL для всех indicators:* ключей. Тот же принцип, что
// system:exchange_ping и system:bot_metrics в bot: если analyzer упал,
// значения должны "протухнуть" в Redis сами, через некоторое время после
// последней публикации — иначе signal-engine (следующий сервис) увидит
// НЕ живые, а последние известные индикаторы и примет решение на основе
// устаревших данных, даже не подозревая об этом.
//
// 60s выбраны по аналогии с system:* в bot. Индикаторы пересчитываются
// каждые calc_interval (по конфигу, например 5s) — если TTL истёк,
// значит publisher не обновлял значение минимум 60s подряд, что
// значительно дольше любого разумного calc_interval и однозначно
// говорит о падении analyzer, а не о случайной задержке одного цикла.
const indicatorTTL = 60 * time.Second

// Publisher пишет рассчитанные T/V/P снапшоты в Redis.
type Publisher struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// PublishTrend записывает T-снапшот для одного символа и одного
// таймфрейма в indicators:trend:{tf}:{symbol}.
func (p *Publisher) PublishTrend(ctx context.Context, tf, symbol string, snap indicator.TrendSnapshot) error {
	key := fmt.Sprintf("indicators:trend:%s:%s", tf, symbol)
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("PublishTrend marshal %s/%s: %w", tf, symbol, err)
	}
	if err := p.rdb.Set(ctx, key, raw, indicatorTTL).Err(); err != nil {
		return fmt.Errorf("PublishTrend %s/%s: %w", tf, symbol, err)
	}
	return nil
}

// PublishVolume записывает V-снапшот для одного символа и одного
// таймфрейма в indicators:volume:{tf}:{symbol}.
func (p *Publisher) PublishVolume(ctx context.Context, tf, symbol string, snap indicator.VolumeSnapshot) error {
	key := fmt.Sprintf("indicators:volume:%s:%s", tf, symbol)
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("PublishVolume marshal %s/%s: %w", tf, symbol, err)
	}
	if err := p.rdb.Set(ctx, key, raw, indicatorTTL).Err(); err != nil {
		return fmt.Errorf("PublishVolume %s/%s: %w", tf, symbol, err)
	}
	return nil
}

// PublishPressure записывает P-снапшот для одного символа в
// indicators:pressure:{symbol}. Без {tf} в ключе — P не привязан к
// таймфрейму (см. согласованное решение и комментарий у
// indicator.PressureSnapshot).
func (p *Publisher) PublishPressure(ctx context.Context, symbol string, snap indicator.PressureSnapshot) error {
	key := fmt.Sprintf("indicators:pressure:%s", symbol)
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("PublishPressure marshal %s: %w", symbol, err)
	}
	if err := p.rdb.Set(ctx, key, raw, indicatorTTL).Err(); err != nil {
		return fmt.Errorf("PublishPressure %s: %w", symbol, err)
	}
	return nil
}
