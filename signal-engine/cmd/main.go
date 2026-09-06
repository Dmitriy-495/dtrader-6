package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Dmitriy-495/dtrader-6/signal-engine/internal/config"
	"github.com/Dmitriy-495/dtrader-6/signal-engine/internal/publisher"
	"github.com/Dmitriy-495/dtrader-6/signal-engine/internal/reader"
	"github.com/Dmitriy-495/dtrader-6/signal-engine/internal/redisclient"
	"github.com/Dmitriy-495/dtrader-6/signal-engine/internal/rules"
)

// pingTimeout — сколько ждать ответа от Redis на старте, прежде чем
// признать его недоступным. Тот же принцип, что и в analyzer/cmd/main.go.
const pingTimeout = 5 * time.Second

func main() {
	fmt.Println("🚀 DTrader 6 Signal-engine запускается...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
	}
	fmt.Printf("✅ Конфиг загружен: %s (%s)\n", cfg.App.Name, cfg.App.Env)
	fmt.Printf("   Символы:    %v\n", cfg.Symbols)
	fmt.Printf("   Таймфреймы: %v\n", cfg.Timeframes)
	fmt.Printf("   Redis:      %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
	fmt.Printf("   Poll:       %s\n", cfg.PollIntervalDuration())
	fmt.Printf("   Staleness:  %s\n", cfg.StalenessThresholdDuration())

	if len(cfg.RulesRaw) == 0 {
		fmt.Println("⚠️  rules: пусто в config.yaml — методология TVP_SNIPER ещё не восстановлена.")
		fmt.Println("⚠️  Все сигналы будут HOLD/rules_not_configured, пока это не изменится (см. internal/rules).")
	}

	rdb := redisclient.New(cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password, cfg.Redis.DB)
	defer rdb.Close()

	pingCtx, cancelPing := context.WithTimeout(context.Background(), pingTimeout)
	err = redisclient.Ping(pingCtx, rdb)
	cancelPing()
	if err != nil {
		log.Fatalf("❌ Redis недоступен: %v", err)
	}
	fmt.Printf("✅ Redis подключён: %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)

	rd := reader.New(rdb, cfg.StalenessThresholdDuration())
	pub := publisher.New(rdb)

	// ctx — главный контекст работы signal-engine, отменяется по
	// SIGINT/SIGTERM. Тот же паттерн graceful shutdown, что и в
	// analyzer/cmd/main.go и bot/cmd/main.go.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Один цикл на символ, каждый в своей горутине — тот же принцип
	// "горутина на символ, независимый цикл", что и Engine в analyzer.
	// В отличие от analyzer, здесь нет накапливаемого между тиками
	// состояния (indicators:* уже самодостаточны), поэтому цикл проще:
	// poll → Evaluate → publish, без mutex и без reader-горутин.
	var wg sync.WaitGroup
	for _, sym := range cfg.Symbols {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()
			log.Printf("▶️  Цикл запущен: %s", symbol)
			runSymbolLoop(ctx, symbol, cfg, rd, pub)
			log.Printf("⏹️  Цикл остановлен: %s", symbol)
		}(sym)
	}

	fmt.Println("✅ Signal-engine запущен! Решения пишутся в signals:*.")

	<-ctx.Done()
	fmt.Println("\n👋 Получен сигнал остановки, завершение работы...")
	wg.Wait()
	fmt.Println("👋 Все циклы остановлены. Выход.")
}

// runSymbolLoop — независимый цикл одного символа: раз в
// cfg.PollIntervalDuration() читает T/V/P из Redis, вызывает
// rules.Evaluate и публикует результат в signals:{symbol}.
//
// Если чтение T/V/P для какого-то таймфрейма провалилось (ErrNotFound
// или ErrStale — см. internal/reader) — это НЕ паникует и не
// останавливает цикл, а публикует HOLD с этой ошибкой как причиной:
// signal-engine не должен молча пропускать тик и не должен пытаться
// принять решение по неполным данным (см. ESTAFETA_SIGNAL.md, раздел 2:
// "не предполагай, что раз indicators:* существуют, значит они
// корректны").
func runSymbolLoop(ctx context.Context, symbol string, cfg *config.Config, rd *reader.Reader, pub *publisher.Publisher) {
	ticker := time.NewTicker(cfg.PollIntervalDuration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick(ctx, symbol, cfg, rd, pub)
		}
	}
}

func tick(ctx context.Context, symbol string, cfg *config.Config, rd *reader.Reader, pub *publisher.Publisher) {
	now := time.Now()

	input, err := gatherInput(ctx, symbol, cfg, rd, now)
	if err != nil {
		log.Printf("⚠️  %s: не удалось собрать T/V/P (%v) — публикую HOLD", symbol, err)
		signal := rules.Signal{Type: rules.SignalHold, Reason: rules.ReasonRulesNotConfigured}
		if pubErr := pub.PublishSignal(ctx, symbol, signal, now.UnixMilli()); pubErr != nil {
			log.Printf("❌ %s: не удалось опубликовать HOLD-сигнал: %v", symbol, pubErr)
		}
		return
	}

	signal := rules.Evaluate(input)
	if err := pub.PublishSignal(ctx, symbol, signal, now.UnixMilli()); err != nil {
		log.Printf("❌ %s: не удалось опубликовать сигнал: %v", symbol, err)
	}
}

// gatherInput читает T (по всем cfg.Timeframes), V (по всем
// cfg.Timeframes) и P (один на символ) и собирает их в rules.Input.
// Возвращает первую встреченную ошибку — на этой стадии (методология
// ещё не восстановлена) неполные данные всё равно приводят к HOLD, так
// что нет смысла собирать все ошибки сразу; как только появятся
// реальные правила, скорее всего потребуется различать "какого именно
// таймфрейма не хватает", и эту функцию нужно будет пересмотреть.
func gatherInput(ctx context.Context, symbol string, cfg *config.Config, rd *reader.Reader, now time.Time) (rules.Input, error) {
	input := rules.Input{
		Trend:  make(map[string]rules.TrendInput, len(cfg.Timeframes)),
		Volume: make(map[string]rules.VolumeInput, len(cfg.Timeframes)),
	}

	for _, tf := range cfg.Timeframes {
		trend, err := rd.FetchTrend(ctx, tf, symbol, now)
		if err != nil {
			return rules.Input{}, fmt.Errorf("trend[%s]: %w", tf, err)
		}
		input.Trend[tf] = rules.TrendInput{
			EMAFast:       trend.EMAFast,
			EMASlow:       trend.EMASlow,
			Direction:     string(trend.Direction),
			Angle:         trend.Angle,
			RSI:           trend.RSI,
			MACDHistogram: trend.MACDHistogram,
		}

		volume, err := rd.FetchVolume(ctx, tf, symbol, now)
		if err != nil {
			return rules.Input{}, fmt.Errorf("volume[%s]: %w", tf, err)
		}
		input.Volume[tf] = rules.VolumeInput{
			BuyVol:  volume.BuyVol,
			SellVol: volume.SellVol,
			Delta:   volume.Delta,
			Spike:   volume.Spike,
		}
	}

	pressure, err := rd.FetchPressure(ctx, symbol, now)
	if err != nil {
		return rules.Input{}, fmt.Errorf("pressure: %w", err)
	}
	input.Pressure = rules.PressureInput{
		BidVol:    pressure.BidVol,
		AskVol:    pressure.AskVol,
		Imbalance: pressure.Imbalance,
	}

	return input, nil
}
