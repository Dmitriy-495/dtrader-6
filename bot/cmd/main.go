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

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
	}

	fmt.Printf("✅ Конфиг загружен: %s (%s)\n", cfg.App.Name, cfg.App.Env)
	fmt.Printf("   Биржа:   %s\n", cfg.Exchange.Name)
	fmt.Printf("   Символы: %v\n", cfg.Symbols)
	fmt.Printf("   Redis:   %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)

	newCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), gateway.RequestTimeout)
	}

	// -------------------------------------------------------------------------
	// REST — проверяем биржу, баланс, позиции
	// -------------------------------------------------------------------------
	client := gateway.NewClient(cfg.Secrets.APIKey, cfg.Secrets.APISecret, cfg.Exchange.RestURL)

	pingCtx, cancelPing := newCtx()
	contractName, err := client.Ping(pingCtx)
	cancelPing()
	if err != nil {
		log.Fatalf("❌ Ping не удался: %v", err)
	}
	fmt.Printf("✅ Биржа доступна. Первый контракт: %s\n", contractName)

	balanceCtx, cancelBalance := newCtx()
	account, err := client.GetUnifiedBalance(balanceCtx)
	cancelBalance()
	if err != nil {
		log.Fatalf("❌ Ошибка получения баланса: %v", err)
	}
	fmt.Printf("✅ Баланс: %s USDT | Маржа: %s USDT | Плечо: x%s\n",
		account.UnifiedAccountTotal,
		account.TotalAvailableMargin,
		account.Leverage,
	)

	posCtx, cancelPos := newCtx()
	positions, err := client.GetPositions(posCtx)
	cancelPos()
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
	// WebSocket — порядок запуска ВАЖЕН:
	// 1. Connect      — устанавливаем соединение
	// 2. ReadLoop     — СРАЗУ начинаем читать, чтобы не пропустить ответы биржи
	// 3. RunPingLoop  — держим соединение живым
	// 4. SubscribeAll — только теперь подписываемся: ReadLoop уже слушает
	// -------------------------------------------------------------------------
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	wsClient := gateway.NewWSClient(cfg.Exchange.WsURL, cfg.Secrets.APIKey, cfg.Secrets.APISecret)

	if err := wsClient.Connect(ctx); err != nil {
		log.Fatalf("❌ WS коннект не удался: %v", err)
	}
	defer wsClient.Close()

	go wsClient.ReadLoop(ctx)
	go wsClient.RunPingLoop(ctx)

	if err := wsClient.SubscribeAll(cfg.Symbols); err != nil {
		log.Fatalf("❌ Ошибка подписки: %v", err)
	}

	fmt.Println("✅ Бот запущен! Собираем данные... (Ctrl+C для остановки)")
	<-ctx.Done()
	fmt.Println("\n👋 Завершение работы...")
}
