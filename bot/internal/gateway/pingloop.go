// Этот файл отвечает ТОЛЬКО за поддержание соединения живым (ping/pong)
// и за измерение латентности до биржи через EMA.
// Здесь нет управления самим соединением (см. connection.go) и нет
// разбора рыночных данных (см. ws.go / будущий parser.go).
package gateway

import (
	"context"
	"log"
	"time"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

// emaAlpha — коэффициент сглаживания EMA (экспоненциальной скользящей
// средней) латентности, рассчитанный на "окно" из 100 периодов.
//
// Формула стандартная для EMA: α = 2 / (N + 1)
// При N = 100:  α = 2 / 101 ≈ 0.0198
//
// Смысл: чем МЕНЬШЕ α, тем ПЛАВНЕЕ EMA реагирует на новые значения —
// один случайный скачок пинга на 500ms не обвалит показатель EXCH
// в TUI, а плавно "размажется" по следующим ~100 замерам.
const emaAlpha = 2.0 / (100.0 + 1.0)

// sendPing отправляет ping-сообщение на Gate.io и запоминает момент
// отправки — это нужно, чтобы посчитать RTT (round-trip time) при
// получении pong в ReadLoop (см. ws.go).
func (c *WSClient) sendPing() error {
	// Запоминаем момент отправки в миллисекундах — при получении pong
	// вычтем это значение из времени получения и получим RTT.
	c.pingTs = time.Now().UnixMilli()
	return c.writeJSON(WSRequest{
		Time:    utils.NowUnix(),
		Channel: "futures.ping",
	})
}

// RunPingLoop запускает бесконечный цикл ping/pong с периодом interval.
// Должен запускаться в отдельной горутине (go wsClient.RunPingLoop(ctx, interval))
// параллельно с ReadLoop — иначе цикл будет блокировать чтение сообщений.
//
// interval берётся из config.yaml (exchange.ping_interval) — раньше был
// захардкожен как 10 секунд прямо здесь, теперь можно менять без
// пересборки бинарника.
//
// Цикл завершается по любому из трёх условий:
//  1. ctx отменён (например, пришёл SIGTERM) — плановое завершение
//  2. c.done просигналил — соединение разорвано где-то ещё (ReadLoop
//     обнаружил обрыв) — нет смысла продолжать пинговать мёртвое соединение
//  3. sendPing вернул ошибку — соединение, видимо, уже не работает
func (c *WSClient) RunPingLoop(ctx context.Context, interval time.Duration) {
	if err := c.sendPing(); err != nil {
		log.Printf("❌ Первый ping не удался: %v", err)
		return
	}
	ticker := time.NewTicker(interval)
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
			// Публикуем счётчик пропущенных публикаций тем же ритмом,
			// что и ping — раз в 10 секунд, без отдельного тикера.
			//
			// Осознанное исключение из общего правила "лог + IncDropped()":
			// если сама публикация метрик не удалась, инкремент счётчика
			// здесь создал бы логическую петлю (метрика о провале публикации
			// метрики) — просто логируем и идём дальше.
			if c.pub != nil {
				if err := c.pub.PublishMetrics(ctx); err != nil {
					log.Printf("⚠️ publish metrics failed: %v", err)
				}
			}
		}
	}
}

// updateEMA пересчитывает экспоненциальную скользящую среднюю латентности
// по формуле: EMA_новое = current × α + EMA_старое × (1 - α)
//
// При самом первом замере (emaLat ещё не инициализирован, равен нулю)
// просто берём текущее значение как стартовую точку — иначе первая EMA
// была бы искусственно занижена (0 × (1-α) исказил бы среднее).
func (c *WSClient) updateEMA(latencyMs int64) {
	current := float64(latencyMs)
	if c.emaLat == 0 {
		// Первое измерение — инициализируем EMA текущим значением
		c.emaLat = current
	} else {
		// EMA = новое × α + старое × (1 - α)
		c.emaLat = current*emaAlpha + c.emaLat*(1-emaAlpha)
	}
}
