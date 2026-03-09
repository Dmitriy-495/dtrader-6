// Точка входа сервиса bot.
// Последовательность запуска:
//   1. Загружаем конфиг (config.yaml + .env)
//   2. Ping — проверяем доступность биржи
//   3. Получаем баланс Unified Account
//   4. Получаем открытые позиции
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/config"
	"github.com/Dmitriy-495/dtrader-6/bot/internal/gateway"
)

func main() {
	// -------------------------------------------------------------------------
	// ШАГ 1: Загружаем конфигурацию.
	// Load читает config.yaml + .env и валидирует все критичные поля.
	// Если что-то не так — падаем здесь с понятной ошибкой.
	// -------------------------------------------------------------------------
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

	// -------------------------------------------------------------------------
	// ШАГ 2: Создаём REST клиент.
	// Все параметры из конфига — никаких магических чисел в коде!
	// -------------------------------------------------------------------------
	fmt.Printf("🔌 Подключаемся к %s...\n", cfg.Exchange.Name)

	client := gateway.NewClient(
		cfg.Secrets.APIKey,
		cfg.Secrets.APISecret,
		cfg.Exchange.RestURL,
	)

	// newCtx — вспомогательная функция для создания контекста с таймаутом.
	// Используем gateway.RequestTimeout — единая константа для всего проекта.
	// Таймаут контекста совпадает с таймаутом HTTP клиента — нет неожиданностей.
	newCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), gateway.RequestTimeout)
	}

	// -------------------------------------------------------------------------
	// ШАГ 3: Ping.
	// -------------------------------------------------------------------------
	fmt.Println("🏓 Отправляем ping...")

	pingCtx, cancelPing := newCtx()
	defer cancelPing()

	contractName, err := client.Ping(pingCtx)
	if err != nil {
		log.Fatalf("❌ Ping не удался: %v", err)
	}
	fmt.Printf("✅ Pong! Биржа доступна. Первый контракт: %s\n", contractName)

	// -------------------------------------------------------------------------
	// ШАГ 4: Баланс Unified Account.
	// -------------------------------------------------------------------------
	fmt.Println("💰 Запрашиваем баланс Unified Account...")

	balanceCtx, cancelBalance := newCtx()
	defer cancelBalance()

	account, err := client.GetUnifiedBalance(balanceCtx)
	if err != nil {
		log.Fatalf("❌ Ошибка получения баланса: %v", err)
	}

	fmt.Printf("✅ Unified Account баланс:\n")
	fmt.Printf("   Общий баланс:        %s USDT\n", account.UnifiedAccountTotal)
	fmt.Printf("   Equity:              %s USDT\n", account.UnifiedAccountTotalEquity)
	fmt.Printf("   Доступная маржа:     %s USDT\n", account.TotalAvailableMargin)
	fmt.Printf("   Начальная маржа:     %s USDT\n", account.TotalInitialMargin)
	fmt.Printf("   Маржа поддержания:   %s USDT\n", account.TotalMaintenanceMargin)
	fmt.Printf("   Текущее плечо:       x%s\n", account.Leverage)

	// Паттерн "запятая ok" — безопасное чтение из map.
	// ok = true если ключ "USDT" найден в map балансов.
	if usdt, ok := account.Balances["USDT"]; ok {
		fmt.Printf("   USDT доступно:       %s USDT\n", usdt.Available)
		fmt.Printf("   USDT cross баланс:   %s USDT\n", usdt.CrossBalance)
		fmt.Printf("   USDT заморожено:     %s USDT\n", usdt.Freeze)
	}

	// -------------------------------------------------------------------------
	// ШАГ 5: Открытые позиции.
	// -------------------------------------------------------------------------
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
			// Направление позиции по знаку Size.
			// Положительный = LONG, отрицательный = SHORT.
			direction := "LONG 📈"
			if p.Size < 0 {
				direction = "SHORT 📉"
			}
			fmt.Printf("   [%d] %s %s\n", i+1, p.Contract, direction)
			fmt.Printf("       Размер:     %d контрактов\n", p.Size)
			fmt.Printf("       Вход:       %s USDT\n", p.EntryPrice)
			fmt.Printf("       Mark:       %s USDT\n", p.MarkPrice)
			fmt.Printf("       PnL:        %s USDT\n", p.UnrealisedPnl)
			fmt.Printf("       Ликвидация: %s USDT\n", p.LiquidationPrice)
			fmt.Printf("       Плечо:      x%d\n", p.Leverage)
		}
	}

	fmt.Println("\n✅ DTrader 6 Bot — начальная инициализация завершена!")
	fmt.Println("⏳ Следующий шаг: подписка на WebSocket стримы...")
}
