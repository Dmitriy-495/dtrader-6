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

	newCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), gateway.RequestTimeout)
	}

	client := gateway.NewClient(cfg.Secrets.APIKey, cfg.Secrets.APISecret, cfg.Exchange.RestURL)
	pingCtx, cancelPing := newCtx()
	contractName, err := client.Ping(pingCtx)
	cancelPing()
	if err != nil {
		log.Fatalf("❌ Ping не удался: %v", err)
	}
	fmt.Printf("✅ Биржа доступна: %s\n", contractName)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	wsClient := gateway.NewWSClient(cfg.Exchange.WsURL, cfg.Secrets.APIKey, cfg.Secrets.APISecret)

	if err := wsClient.Connect(ctx); err != nil {
		log.Fatalf("❌ WS коннект не удался: %v", err)
	}
	defer wsClient.Close()

	go wsClient.ReadLoop(ctx)
	go wsClient.RunPingLoop(ctx)

	if err := wsClient.SubscribeTickers([]string{"BTC_USDT"}); err != nil {
		log.Fatalf("❌ Ошибка подписки на tickers: %v", err)
	}

	fmt.Println("✅ Бот запущен! tickers BTC_USDT + ping/pong... (Ctrl+C для остановки)")
	<-ctx.Done()
	fmt.Println("\n👋 Завершение работы...")
}
