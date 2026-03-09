// Этот файл реализует высокоуровневые методы REST API Gate.io.
package gateway

import (
	"context"
	"fmt"
)

// Contract — минимальная структура контракта Gate.io для Ping.
type Contract struct {
	Name      string `json:"name"`
	MarkPrice string `json:"mark_price"`
}

// CurrencyBalance — баланс по одной валюте внутри Unified Account.
// Вложенная структура внутри поля balances:{USDT:{...}, BTC:{...}}
type CurrencyBalance struct {
	// Available — доступный баланс валюты
	Available string `json:"available"`

	// AvailableMargin — доступная маржа для торговли
	AvailableMargin string `json:"available_margin"`

	// CrossBalance — баланс в Cross Margin режиме
	CrossBalance string `json:"cross_balance"`

	// MarginBalance — маржинальный баланс с учётом PnL
	MarginBalance string `json:"margin_balance"`

	// Equity — собственный капитал по этой валюте
	Equity string `json:"equity"`

	// Freeze — замороженные средства (в ордерах)
	Freeze string `json:"freeze"`

	// Borrowed — заёмные средства
	Borrowed string `json:"borrowed"`
}

// UnifiedAccount — структура ответа GET /unified/accounts.
// Поля взяты из реального RAW JSON ответа Gate.io.
type UnifiedAccount struct {
	// UnifiedAccountTotal — общий баланс Unified Account в USDT эквиваленте.
	// Это главное поле — показывает реальный общий баланс.
	UnifiedAccountTotal string `json:"unified_account_total"`

	// UnifiedAccountTotalEquity — общий equity в USDT эквиваленте.
	UnifiedAccountTotalEquity string `json:"unified_account_total_equity"`

	// UnifiedAccountTotalLiab — общие обязательства (долги).
	UnifiedAccountTotalLiab string `json:"unified_account_total_liab"`

	// TotalMarginBalance — общий маржинальный баланс
	TotalMarginBalance string `json:"total_margin_balance"`

	// TotalAvailableMargin — общая доступная маржа для новых позиций
	TotalAvailableMargin string `json:"total_available_margin"`

	// TotalInitialMargin — общая начальная маржа по открытым позициям
	TotalInitialMargin string `json:"total_initial_margin"`

	// TotalMaintenanceMargin — маржа поддержания (ниже = ликвидация!)
	TotalMaintenanceMargin string `json:"total_maintenance_margin"`

	// Leverage — текущее плечо аккаунта
	Leverage string `json:"leverage"`

	// Balances — балансы по каждой валюте.
	// map[string]CurrencyBalance — словарь где ключ = название валюты (USDT, BTC...)
	// и значение = структура с балансами по этой валюте.
	Balances map[string]CurrencyBalance `json:"balances"`
}

// Position — структура открытой позиции Gate.io.
type Position struct {
	Contract         string `json:"contract"`
	Size             int64  `json:"size"`
	EntryPrice       string `json:"entry_price"`
	MarkPrice        string `json:"mark_price"`
	UnrealisedPnl    string `json:"unrealised_pnl"`
	Margin           string `json:"margin"`
	LiquidationPrice string `json:"liq_price"`
	Leverage         int64  `json:"leverage"`
	Mode             string `json:"mode"`
}

// =============================================================================
// МЕТОДЫ REST API
// =============================================================================

// Ping проверяет доступность биржи Gate.io через публичный endpoint.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var contracts []Contract

	err := c.GetPublic(ctx, "/futures/usdt/contracts", "limit=1", &contracts)
	if err != nil {
		return "", fmt.Errorf("ping Gate.io не удался: %w", err)
	}

	if len(contracts) == 0 {
		return "", fmt.Errorf("ping Gate.io: биржа вернула пустой список контрактов")
	}

	return contracts[0].Name, nil
}

// GetUnifiedBalance возвращает баланс Unified Account.
// Endpoint: GET /unified/accounts
func (c *Client) GetUnifiedBalance(ctx context.Context) (*UnifiedAccount, error) {
	var account UnifiedAccount

	err := c.Get(ctx, "/unified/accounts", "", &account)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения Unified баланса: %w", err)
	}

	return &account, nil
}

// GetPositions возвращает список активных открытых позиций.
// Endpoint: GET /futures/usdt/positions
func (c *Client) GetPositions(ctx context.Context) ([]Position, error) {
	var positions []Position

	err := c.Get(ctx, "/futures/usdt/positions", "", &positions)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения позиций: %w", err)
	}

	active := make([]Position, 0)
	for _, p := range positions {
		if p.Size != 0 {
			active = append(active, p)
		}
	}

	return active, nil
}
