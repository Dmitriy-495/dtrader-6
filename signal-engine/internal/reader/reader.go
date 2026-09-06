// Пакет reader читает indicators:* из Redis (пишет их analyzer) и
// отдаёт их в виде типизированных снапшотов (см. types.go). Единственная
// точка чтения signal-engine из Redis — по прямой аналогии с
// analyzer/internal/reader (читает market:*) и publisher (единственная
// точка записи).
//
// indicators:* — это Redis String с TTL, НЕ Stream (см. ESTAFETA_SIGNAL.md,
// раздел "Шаг 3" — "indicators — это String с TTL, не Stream, poll здесь
// единственный вариант"). Поэтому reader — это просто Get + Unmarshal на
// каждый вызов, без буферизации состояния между вызовами (в отличие от
// analyzer/internal/engine, которому нужно накапливать trades/orderbook
// между тиками — signal-engine такой необходимости не имеет, каждый
// снапшот из Redis уже самодостаточен).
package reader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound возвращается, когда ключ отсутствует в Redis (TTL истёк
// или ключ никогда не публиковался analyzer'ом). Отдельный sentinel-error,
// а не просто redis.Nil напрямую — чтобы вызывающий код (rules/ и любой
// будущий health-check) мог отличить "данных нет вовсе" от "данные есть,
// но устарели" (см. ErrStale) без знания деталей клиента Redis.
var ErrNotFound = errors.New("indicators: ключ не найден в Redis")

// ErrStale возвращается, когда ключ найден, но его поле ts отстаёт от
// текущего момента больше, чем на допустимый staleness threshold — то
// есть analyzer, судя по всему, не пишет свежие данные (упал, завис,
// или bot недоступен), хотя TTL ключа ещё не истёк. Это ровно тот
// случай, о котором явно предупреждает ESTAFETA_SIGNAL.md (раздел 2,
// урок из деплоя на sgp): наличие ключа само по себе не означает, что
// данные в нём корректны и свежи.
var ErrStale = errors.New("indicators: данные устарели (ts отстаёт больше допустимого)")

// Reader читает indicators:* из Redis.
type Reader struct {
	rdb       *redis.Client
	staleness time.Duration
}

// New создаёт Reader. staleness — максимально допустимое отставание
// поля ts снапшота от текущего момента, после которого снапшот
// считается устаревшим (см. Config.StalenessThresholdDuration()).
func New(rdb *redis.Client, staleness time.Duration) *Reader {
	return &Reader{rdb: rdb, staleness: staleness}
}

// checkFreshness проверяет, что ts (unix ms) не отстаёт от текущего
// момента больше, чем на r.staleness. now передаётся параметром (а не
// берётся через time.Now() внутри) — тот же принцип, что и в
// analyzer/internal/indicator.CalcTrend: чистая, легко тестируемая
// логика без скрытых побочных эффектов.
func (r *Reader) checkFreshness(ts int64, now time.Time) error {
	snapTime := time.UnixMilli(ts)
	age := now.Sub(snapTime)
	if age > r.staleness {
		return fmt.Errorf("%w: age=%s, threshold=%s", ErrStale, age, r.staleness)
	}
	return nil
}

// getRaw — общая часть Get у всех Fetch*-методов ниже: достаёт сырой
// JSON по ключу и превращает redis.Nil в ErrNotFound. Разбор JSON и
// проверка свежести ts остаются в каждом Fetch* отдельно, потому что
// тип снапшота у каждого свой.
func (r *Reader) getRaw(ctx context.Context, key string) ([]byte, error) {
	raw, err := r.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return nil, fmt.Errorf("indicators: Get %s: %w", key, err)
	}
	return raw, nil
}

// FetchTrend читает indicators:trend:{tf}:{symbol} и проверяет
// свежесть ts относительно now.
func (r *Reader) FetchTrend(ctx context.Context, tf, symbol string, now time.Time) (TrendSnapshot, error) {
	key := fmt.Sprintf("indicators:trend:%s:%s", tf, symbol)
	raw, err := r.getRaw(ctx, key)
	if err != nil {
		return TrendSnapshot{}, err
	}

	var snap TrendSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return TrendSnapshot{}, fmt.Errorf("indicators: Unmarshal %s: %w", key, err)
	}

	if err := r.checkFreshness(snap.Ts, now); err != nil {
		return snap, fmt.Errorf("%s: %w", key, err)
	}
	return snap, nil
}

// FetchVolume читает indicators:volume:{tf}:{symbol} и проверяет
// свежесть ts относительно now.
func (r *Reader) FetchVolume(ctx context.Context, tf, symbol string, now time.Time) (VolumeSnapshot, error) {
	key := fmt.Sprintf("indicators:volume:%s:%s", tf, symbol)
	raw, err := r.getRaw(ctx, key)
	if err != nil {
		return VolumeSnapshot{}, err
	}

	var snap VolumeSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return VolumeSnapshot{}, fmt.Errorf("indicators: Unmarshal %s: %w", key, err)
	}

	if err := r.checkFreshness(snap.Ts, now); err != nil {
		return snap, fmt.Errorf("%s: %w", key, err)
	}
	return snap, nil
}

// FetchPressure читает indicators:pressure:{symbol} (без {tf} в ключе —
// P не привязан к таймфрейму, см. reader/types.go) и проверяет
// свежесть ts относительно now.
func (r *Reader) FetchPressure(ctx context.Context, symbol string, now time.Time) (PressureSnapshot, error) {
	key := fmt.Sprintf("indicators:pressure:%s", symbol)
	raw, err := r.getRaw(ctx, key)
	if err != nil {
		return PressureSnapshot{}, err
	}

	var snap PressureSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return PressureSnapshot{}, fmt.Errorf("indicators: Unmarshal %s: %w", key, err)
	}

	if err := r.checkFreshness(snap.Ts, now); err != nil {
		return snap, fmt.Errorf("%s: %w", key, err)
	}
	return snap, nil
}
