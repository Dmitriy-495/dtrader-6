package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/config"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/gateway"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/publisher"
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

	pub := publisher.New(cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password)
	pingCtx, cancelPing := context.WithTimeout(context.Background(), gateway.RequestTimeout)
	if err := pub.Ping(pingCtx); err != nil {
		log.Fatalf("❌ Redis недоступен: %v", err)
	}
	cancelPing()
	fmt.Printf("✅ Redis подключён: %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
	defer pub.Close()

	newCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), gateway.RequestTimeout)
	}

	client := gateway.NewClient(cfg.Secrets.APIKey, cfg.Secrets.APISecret, cfg.Exchange.RestURL)

	restPingCtx, cancelRestPing := newCtx()
	contractName, err := client.Ping(restPingCtx)
	cancelRestPing()
	if err != nil {
		log.Fatalf("❌ Ping не удался: %v", err)
	}
	fmt.Printf("✅ Биржа доступна: %s\n", contractName)

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

	// Пишем баланс в Redis — ws-server транслирует в TUI
	balPubCtx, cancelBalPub := newCtx()
	if err := pub.PublishBalance(balPubCtx,
		account.UnifiedAccountTotal,
		account.TotalAvailableMargin,
		account.Leverage,
	); err != nil {
		log.Printf("⚠️ Не удалось записать баланс в Redis: %v", err)
	}
	cancelBalPub()

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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	wsClient := gateway.NewWSClient(cfg.Exchange.WsURL, cfg.Secrets.APIKey, cfg.Secrets.APISecret, pub)

	subscribeAll := func() error {
		if err := wsClient.SubscribeTrades(cfg.Symbols); err != nil {
			return fmt.Errorf("trades: %w", err)
		}
		if err := wsClient.SubscribeOrderBookUpdate(cfg.Symbols); err != nil {
			return fmt.Errorf("order_book_update: %w", err)
		}
		if err := wsClient.SubscribeCandlesticks(cfg.Symbols); err != nil {
			return fmt.Errorf("candlesticks: %w", err)
		}
		if err := wsClient.SubscribePublicLiquidates(cfg.Symbols); err != nil {
			return fmt.Errorf("public_liquidates: %w", err)
		}
		if err := wsClient.SubscribeContractStats(cfg.Symbols); err != nil {
			return fmt.Errorf("contract_stats: %w", err)
		}
		return nil
	}

	for {
		wsClient.ResetDone()

		if err := wsClient.Connect(ctx); err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("❌ WS коннект не удался: %v — повтор через 5 сек", err)
			select {
			case <-ctx.Done():
				goto shutdown
			case <-time.After(5 * time.Second):
				continue
			}
		}

		go wsClient.ReadLoop(ctx)
		go wsClient.RunPingLoop(ctx)

		if err := subscribeAll(); err != nil {
			log.Printf("❌ Ошибка подписки: %v — реконнект через 5 сек", err)
			wsClient.Close()
			select {
			case <-ctx.Done():
				goto shutdown
			case <-time.After(5 * time.Second):
				continue
			}
		}

		log.Println("✅ Бот запущен! Данные пишутся в Redis.")

		select {
		case <-ctx.Done():
			goto shutdown
		case <-wsClient.Done():
			log.Println("🔄 WS разорван. Реконнект через 5 сек...")
			wsClient.Close()
			select {
			case <-ctx.Done():
				goto shutdown
			case <-time.After(5 * time.Second):
			}
		}
	}

shutdown:
	fmt.Println("\n👋 Завершение работы...")
	wsClient.Close()
}
