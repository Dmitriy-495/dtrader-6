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

	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/config"
	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/engine"
	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/redisclient"
)

// pingTimeout — сколько ждать ответа от Redis на старте, прежде чем
// признать его недоступным. Analyzer не ходит никуда, кроме Redis,
// поэтому единственная "внешняя" проверка при старте — именно эта.
const pingTimeout = 5 * time.Second

func main() {
	fmt.Println("🚀 DTrader 6 Analyzer запускается...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
	}
	fmt.Printf("✅ Конфиг загружен: %s (%s)\n", cfg.App.Name, cfg.App.Env)
	fmt.Printf("   Символы:    %v\n", cfg.Symbols)
	fmt.Printf("   Таймфреймы: %v\n", cfg.AllTimeframes())
	fmt.Printf("   Redis:      %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)

	rdb := redisclient.New(cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password, cfg.Redis.DB)
	defer rdb.Close()

	// Отдельный короткоживущий контекст только для Ping — не хотим, чтобы
	// проверка на старте могла зависнуть навсегда, если Redis недоступен.
	// Тот же принцип, что и pingCtx/cancelPing в bot/cmd/main.go.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), pingTimeout)
	err = redisclient.Ping(pingCtx, rdb)
	cancelPing()
	if err != nil {
		log.Fatalf("❌ Redis недоступен: %v", err)
	}
	fmt.Printf("✅ Redis подключён: %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)

	// ctx — главный контекст работы analyzer. Отменяется по SIGINT/SIGTERM
	// (Ctrl+C или systemd stop) — тот же паттерн graceful shutdown, что и
	// в bot/cmd/main.go (signal.NotifyContext), только без WS-реконнекта:
	// у analyzer нет соединения с биржей, только с Redis, и go-redis сам
	// переживает временные обрывы соединения без явной логики реконнекта
	// в этом коде.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Один Engine на символ, каждый в своей горутине — согласно принятому
	// решению "горутина на символ, независимый цикл". wg.Wait() блокирует
	// main() до тех пор, пока ВСЕ Engine не завершатся (что происходит
	// только после отмены ctx — см. engine.Run).
	var wg sync.WaitGroup
	for _, symbol := range cfg.Symbols {
		eng := engine.New(symbol, cfg, rdb)
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			log.Printf("▶️  Engine запущен: %s", sym)
			eng.Run(ctx)
			log.Printf("⏹️  Engine остановлен: %s", sym)
		}(symbol)
	}

	fmt.Println("✅ Analyzer запущен! Индикаторы пишутся в indicators:*.")

	<-ctx.Done()
	fmt.Println("\n👋 Получен сигнал остановки, завершение работы...")
	wg.Wait()
	fmt.Println("👋 Все Engine остановлены. Выход.")
}
