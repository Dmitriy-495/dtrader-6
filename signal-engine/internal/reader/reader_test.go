package reader

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// realTrend1m/realVolume1m/realPressure — реальные числа из живого
// прода (см. dtrader-tui-6/internal/tui/symbol_test.go,
// realIndicatorsPayload, лог TUI шаг 1, 2026-08-17 23:33 MSK,
// BTC_USDT), а не подобранные вручную — по прямой рекомендации
// PROMPT_NEXT.md тестировать на реальных, а не выдуманных данных.
const (
	realTs = int64(1786998812534) // unix ms из реального снапшота

	realTrend1mJSON = `{"ema_fast":64336.7819116163,"ema_slow":64316.64902869316,"direction":"neutral","angle":79.5419837407385,"rsi":62.968099861304914,"macd_histogram":0,"ts":1786998812534}`

	realVolume1mJSON = `{"buy_vol":17718,"sell_vol":0,"delta":17718,"spike":false,"ts":1786998812534}`

	realPressureJSON = `{"bid_vol":41053,"ask_vol":79193,"imbalance":0.518391777051002,"ts":1786998812534}`
)

// testRedisAddr — адрес реального Redis, используемого в тестах этого
// пакета. По умолчанию отдельный тестовый инстанс на нестандартном
// порту (см. описание запуска в PROMPT_NEXT.md для этой сессии),
// переопределяем через TEST_REDIS_ADDR, если понадобится указать
// другой адрес (например в CI).
//
// Сознательно НЕ используется miniredis (in-memory эмулятор без
// реального Redis) — попытка подключить его через vendor-src упёрлась
// в транзитивную тестовую зависимость gopher-lua → gopkg.in/check.v1,
// которая недоступна в сетевом allowlist песочницы. Настоящий
// redis-server, поднятый локально, даёт даже более честную проверку:
// тест идёт по тому же протоколу go-redis/v9, что и боевой код, без
// эмуляции команд.
func testRedisAddr() string {
	if addr := os.Getenv("TEST_REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:16379"
}

// testDBCounter — счётчик для выбора отдельной логической БД Redis под
// каждый тест (SELECT 0..15), чтобы параллельные/последовательные
// тесты не видели ключи друг друга без явного FLUSHDB в каждом.
// Redis по умолчанию даёт 16 баз (0-15) — этого достаточно для этого
// пакета; при желании увеличить параллелизм тестов позже, можно
// перейти на общий префикс ключей вместо номера БД.
var testDBCounter int32

// newTestReader подключается к реальному testRedisAddr(), выбирает
// свежую логическую БД (FLUSHDB перед использованием — на случай,
// если предыдущий прогон тестов упал и не почистил за собой) и
// возвращает Reader поверх неё вместе с самим клиентом go-redis, чтобы
// тесты могли писать тестовые данные напрямую через Set — тем же
// способом, каким analyzer пишет их в проде (json.Marshal + SET с
// TTL), а не через отдельный API эмулятора.
func newTestReader(t *testing.T, staleness time.Duration) (*Reader, *redis.Client) {
	t.Helper()

	db := int(atomic.AddInt32(&testDBCounter, 1)) % 16
	rdb := redis.NewClient(&redis.Options{Addr: testRedisAddr(), DB: db})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("не удалось подключиться к тестовому Redis (%s): %v — он должен быть запущен отдельно для этих тестов", testRedisAddr(), err)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("не удалось очистить тестовую БД Redis: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = rdb.FlushDB(cleanupCtx).Err()
		_ = rdb.Close()
	})

	return New(rdb, staleness), rdb
}

// realNow — момент времени, который считается "сейчас" относительно
// realTs при построении тестов на успешное, свежее чтение: ровно
// realTs + 1 секунда, то есть заведомо внутри любого разумного
// staleness_threshold (config.yaml по умолчанию — 20s).
func realNow() time.Time {
	return time.UnixMilli(realTs).Add(1 * time.Second)
}

// setKey кладёт value под key в тестовый Redis через тот же go-redis
// клиент и тот же метод (SET), которым analyzer/internal/publisher
// реально пишет indicators:* в проде — TTL здесь не важен (0 = без
// истечения), тесты сами живут секунды, а не минуты.
func setKey(t *testing.T, rdb *redis.Client, key, value string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Set(ctx, key, value, 0).Err(); err != nil {
		t.Fatalf("не удалось записать тестовый ключ %s: %v", key, err)
	}
}

func TestFetchTrend_RealProdPayload(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:trend:1m:BTC_USDT", realTrend1mJSON)

	snap, err := rd.FetchTrend(context.Background(), "1m", "BTC_USDT", realNow())
	if err != nil {
		t.Fatalf("FetchTrend вернул ошибку на свежих реальных данных: %v", err)
	}

	// Числа взяты буквально из реального прод-снапшота — если они не
	// совпадают, значит либо JSON-теги разъехались с analyzer, либо
	// разбор сломан.
	if snap.EMAFast != 64336.7819116163 {
		t.Errorf("EMAFast = %v, хотим 64336.7819116163", snap.EMAFast)
	}
	if snap.EMASlow != 64316.64902869316 {
		t.Errorf("EMASlow = %v, хотим 64316.64902869316", snap.EMASlow)
	}
	if snap.Direction != DirectionNeutral {
		t.Errorf("Direction = %q, хотим %q", snap.Direction, DirectionNeutral)
	}
	if snap.RSI != 62.968099861304914 {
		t.Errorf("RSI = %v, хотим 62.968099861304914", snap.RSI)
	}
	if snap.MACDHistogram != 0 {
		t.Errorf("MACDHistogram = %v, хотим 0 (MACD выключен на 1m)", snap.MACDHistogram)
	}
	if snap.Ts != realTs {
		t.Errorf("Ts = %d, хотим %d", snap.Ts, realTs)
	}
}

func TestFetchVolume_RealProdPayload(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:volume:1m:BTC_USDT", realVolume1mJSON)

	snap, err := rd.FetchVolume(context.Background(), "1m", "BTC_USDT", realNow())
	if err != nil {
		t.Fatalf("FetchVolume вернул ошибку на свежих реальных данных: %v", err)
	}

	if snap.BuyVol != 17718 {
		t.Errorf("BuyVol = %v, хотим 17718", snap.BuyVol)
	}
	if snap.SellVol != 0 {
		t.Errorf("SellVol = %v, хотим 0", snap.SellVol)
	}
	if snap.Delta != 17718 {
		t.Errorf("Delta = %v, хотим 17718", snap.Delta)
	}
	if snap.Spike {
		t.Error("Spike = true, хотим false")
	}
}

func TestFetchPressure_RealProdPayload(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:pressure:BTC_USDT", realPressureJSON)

	snap, err := rd.FetchPressure(context.Background(), "BTC_USDT", realNow())
	if err != nil {
		t.Fatalf("FetchPressure вернул ошибку на свежих реальных данных: %v", err)
	}

	if snap.BidVol != 41053 {
		t.Errorf("BidVol = %v, хотим 41053", snap.BidVol)
	}
	if snap.AskVol != 79193 {
		t.Errorf("AskVol = %v, хотим 79193", snap.AskVol)
	}
	if snap.Imbalance != 0.518391777051002 {
		t.Errorf("Imbalance = %v, хотим 0.518391777051002", snap.Imbalance)
	}
}

// TestFetchTrend_NotFound проверяет поведение, когда ключ вообще
// отсутствует в Redis (TTL истёк или analyzer никогда его не
// публиковал) — должна вернуться именно ErrNotFound, а не redis.Nil
// напрямую и не общая ошибка без возможности различить причину.
func TestFetchTrend_NotFound(t *testing.T) {
	rd, _ := newTestReader(t, 20*time.Second)

	_, err := rd.FetchTrend(context.Background(), "1m", "BTC_USDT", realNow())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили: %v", err)
	}
}

// TestFetchTrend_Stale — ключ ЕСТЬ в Redis (TTL не истёк), но его ts
// сильно отстаёт от текущего момента. Это ровно сценарий, о котором
// явно предупреждает ESTAFETA_SIGNAL.md: наличие ключа само по себе
// не означает, что данные свежие — analyzer мог упасть, оставив
// последнее известное значение висеть в Redis.
func TestFetchTrend_Stale(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:trend:1m:BTC_USDT", realTrend1mJSON)

	// "Сейчас" — на 5 минут позже, чем ts в снапшоте: далеко за
	// пределами staleness_threshold в 20s.
	farFuture := time.UnixMilli(realTs).Add(5 * time.Minute)

	_, err := rd.FetchTrend(context.Background(), "1m", "BTC_USDT", farFuture)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("ожидали ErrStale, получили: %v", err)
	}
}

// TestFetchTrend_ExactlyAtThreshold проверяет граничный случай: age
// РОВНО равен staleness — по документации checkFreshness (age >
// r.staleness) это ещё НЕ устарело, граница включительно свежая.
// Явный тест на границу — потому что "> vs >=" здесь легко перепутать
// молча, а разница в один тик calc_interval может быть на практике
// значимой.
func TestFetchTrend_ExactlyAtThreshold(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:trend:1m:BTC_USDT", realTrend1mJSON)

	exactlyAtThreshold := time.UnixMilli(realTs).Add(20 * time.Second)

	_, err := rd.FetchTrend(context.Background(), "1m", "BTC_USDT", exactlyAtThreshold)
	if err != nil {
		t.Fatalf("age == threshold должен считаться ещё свежим, получили ошибку: %v", err)
	}
}

// TestFetchTrend_MalformedJSON проверяет, что повреждённый JSON даёт
// понятную ошибку разбора, а не панику и не тихо нулевой TrendSnapshot.
func TestFetchTrend_MalformedJSON(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:trend:1m:BTC_USDT", `{not valid json`)

	_, err := rd.FetchTrend(context.Background(), "1m", "BTC_USDT", realNow())
	if err == nil {
		t.Fatal("ожидали ошибку разбора JSON, получили nil")
	}
}

// TestFetchPressure_KeyHasNoTimeframe проверяет, что P читается по
// ключу БЕЗ {tf} (indicators:pressure:{symbol}), в отличие от T и V —
// согласованное архитектурное решение (см. reader/types.go), а не
// недосмотр. Если бы reader ошибочно пытался читать
// indicators:pressure:1m:BTC_USDT, этот тест бы упал с ErrNotFound.
func TestFetchPressure_KeyHasNoTimeframe(t *testing.T) {
	rd, rdb := newTestReader(t, 20*time.Second)
	setKey(t, rdb, "indicators:pressure:ETH_USDT", realPressureJSON)

	_, err := rd.FetchPressure(context.Background(), "ETH_USDT", realNow())
	if err != nil {
		t.Fatalf("FetchPressure должен читать indicators:pressure:{symbol} без {tf}: %v", err)
	}
}
