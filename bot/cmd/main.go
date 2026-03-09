package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/config"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/gateway"
)

func main() {
	fmt.Println("🚀 DTrader 6 Bot запускается...")
	fmt.Println("📋 Загружаем конфигурацию...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
	}

	fmt.Printf("✅ Конфиг загружен:\n")
	fmt.Printf("   Сервис:  %s (%s)\n", cfg.App.Name, cfg.App.Env)
	fmt.Printf("   Биржа:   %s\n", cfg.Exchange.Name)
	fmt.Printf("   Символы: %v\n", cfg.Symbols)
	fmt.Printf("   Redis:   %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
	fmt.Println("✅ API ключи загружены")

	// newCtx — контекст с таймаутом для REST запросов
	newCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), gateway.RequestTimeout)
	}

	// -------------------------------------------------------------------------
	// REST: инициализация
	// -------------------------------------------------------------------------
	fmt.Printf("🔌 Подключаемся к %s...\n", cfg.Exchange.Name)
	client := gateway.NewClient(cfg.Secrets.APIKey, cfg.Secrets.APISecret, cfg.Exchange.RestURL)

	// Ping
	fmt.Println("🏓 Отправляем ping...")
	pingCtx, cancelPing := newCtx()
	defer cancelPing()
	contractName, err := client.Ping(pingCtx)
	if err != nil {
		log.Fatalf("❌ Ping не удался: %v", err)
	}
	fmt.Printf("✅ Pong! Биржа доступна. Первый контракт: %s\n", contractName)

	// Баланс
	fmt.Println("💰 Запрашиваем баланс Unified Account...")
	balanceCtx, cancelBalance := newCtx()
	defer cancelBalance()
	account, err := client.GetUnifiedBalance(balanceCtx)
	if err != nil {
		log.Fatalf("❌ Ошибка получения баланса: %v", err)
	}
	fmt.Printf("✅ Unified Account баланс:\n")
	fmt.Printf("   Общий баланс:      %s USDT\n", account.UnifiedAccountTotal)
	fmt.Printf("   Equity:            %s USDT\n", account.UnifiedAccountTotalEquity)
	fmt.Printf("   Доступная маржа:   %s USDT\n", account.TotalAvailableMargin)
	fmt.Printf("   Текущее плечо:     x%s\n", account.Leverage)
	if usdt, ok := account.Balances["USDT"]; ok {
		fmt.Printf("   USDT доступно:     %s USDT\n", usdt.Available)
		fmt.Printf("   USDT заморожено:   %s USDT\n", usdt.Freeze)
	}

	// Позиции
	fmt.Println("📊 Запрашиваем открытые позиции...")
	posCtx, cancelPos := newCtx()
	defer cancelPos()
	positions, err := client.GetPositions(posCtx)
	if err != nil {
		log.Fatalf("❌ Ошибка получения позиций: %v", err)
	}
	if len(positions) == 0 {
		fmt.Println("✅ Открытых позиций нет")
	} else {
		fmt.Printf("✅ Открытые позиции (%d):\n", len(positions))
		for i, p := range positions {
			direction := "LONG 📈"
			if p.Size < 0 {
				direction = "SHORT 📉"
			}
			fmt.Printf("   [%d] %s %s | Вход: %s | PnL: %s\n",
				i+1, p.Contract, direction, p.EntryPrice, p.UnrealisedPnl)
		}
	}

	// -------------------------------------------------------------------------
	// WebSocket
	// -------------------------------------------------------------------------
	fmt.Println("🔌 Подключаемся по WebSocket...")

	// Корневой контекст — живёт до Ctrl+C
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	wsClient := gateway.NewWSClient(cfg.Exchange.WsURL, cfg.Secrets.APIKey, cfg.Secrets.APISecret)
	if err := wsClient.Connect(ctx); err != nil {
		log.Fatalf("❌ WS коннект не удался: %v", err)
	}
	defer wsClient.Close()

	go wsClient.RunPingLoop(ctx)
	go wsClient.ReadLoop(ctx)

	fmt.Println("✅ WS запущен! (Ctrl+C для остановки)")
	<-ctx.Done()
	fmt.Println("\n👋 Завершение работы...")
}
