package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"
	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/config"
	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/handler"
	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/hub"
	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/reader"
)

func main() {
	fmt.Println("🚀 DTrader 6 WS Server запускается...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфига: %v", err)
	}
	fmt.Printf("✅ Конфиг загружен: %s (%s)\n", cfg.App.Name, cfg.App.Env)
	fmt.Printf("   Порт:    %d\n", cfg.Server.Port)
	fmt.Printf("   Символы: %v\n", cfg.Symbols)
	fmt.Printf("   Redis:   %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)

	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Redis недоступен: %v", err)
	}
	fmt.Printf("✅ Redis подключён: %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
	defer rdb.Close()

	h := hub.New()
	go h.Run()

	r := reader.New(rdb, h, cfg.Symbols)
	r.RunAll(ctx)

	wsHandler := handler.New(h, cfg.Secrets.APIKey)
	http.Handle("/ws", wsHandler)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{Addr: addr}

	go func() {
		fmt.Printf("✅ WS Server слушает ws://0.0.0.0%s/ws\n", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ HTTP server error: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("\n👋 Завершение работы...")
	srv.Shutdown(context.Background())
}
