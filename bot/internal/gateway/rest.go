// Этот файл реализует высокоуровневые методы REST API Gate.io.
package gateway

import (
	"context"
	"encoding/json"
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

// OBLevelREST — один уровень стакана В ФОРМАТЕ REST-ОТВЕТА Gate.io.
//
// ⚠️ ВАЖНОЕ ОТЛИЧИЕ ОТ WS: в протоколе futures.order_book_update (WS)
// поле size приходит как JSON-СТРОКА (см. OBLevel в protocol.go, "p"/"s"
// оба string). А вот в REST-ответе GET /futures/usdt/order_book Gate.io
// шлёт size как JSON-ЧИСЛО, не строку — это подтверждено на практике
// (реальный ответ биржи на pre-prod сервере msk дал ошибку
// "cannot unmarshal number into Go struct field OBLevel.asks.s of type
// string", когда мы по ошибке предположили, что формат одинаков для
// WS и REST). Отсюда — два разных типа, не один общий OBLevel.
//
// Price остаётся строкой в обоих случаях (это подтверждено, ошибка была
// именно и только на size) — JSON-число с плавающей точкой, отформатированное
// как обычная цена ("100.5"), Go спокойно разбирает и как число, и как
// строку, поэтому расхождение проявилось только на size, не на price.
//
// json.Number (не float64 напрямую) выбран по той же причине, что и в
// ContractStats (см. protocol.go): даёт доступ к точному числу через
// .Float64()/.String(), но не привязывается жёстко к одному JSON-представлению —
// если Gate.io когда-нибудь в одном ответе смешает число и строку для
// разных уровней (наблюдалось для других полей API Gate.io), json.Number
// разберёт оба варианта без паники, а float64 упал бы на JSON-строке.
type OBLevelREST struct {
	Price string      `json:"p"`
	Size  json.Number `json:"s"`
}

// OrderBookSnapshot — структура ответа GET /futures/usdt/order_book?with_id=true.
// Поля списаны с реального ответа биржи (см. комментарий у OBLevelREST про
// расхождение типов между WS и REST) — НЕ идентичны официальному Go SDK
// gateapi-go/model_futures_order_book.go в части Asks/Bids: тот SDK
// использует []FuturesOrderBookItem, но точный тип этого элемента не
// удалось подтвердить из документации, а прямая проверка на боевом
// сервере (msk) однозначно показала json-число для size — доверяем
// фактическому поведению API, а не предположению по аналогии с WS.
//
// Именно с этого снапшота начинается локальный стакан (см. orderbook.go):
// REST даёт "точку опоры" с конкретным ID, дальше WS-дельты (order_book_update)
// применяются поверх неё. Без снапшота дельты применять не к чему — дельта
// говорит только "что изменилось", а не "что было".
type OrderBookSnapshot struct {
	// ID — идентификатор состояния стакана на момент снапшота.
	// Он же (со сдвигом +1) должен совпасть с полем U одной из входящих
	// WS-дельт — это и есть "точка стыковки" снапшота с потоком дельт.
	// Присутствует в ответе, только если запрос сделан с with_id=true.
	ID int64 `json:"id"`
	// Current — момент генерации ответа (unix seconds, по документации Gate.io).
	Current float64 `json:"current"`
	// Update — момент последнего изменения стакана на момент снапшота.
	Update float64 `json:"update"`
	// Asks/Bids — уровни В ФОРМАТЕ REST (OBLevelREST, не OBLevel!) —
	// см. комментарий у OBLevelREST, почему это разные типы.
	Asks []OBLevelREST `json:"asks"`
	Bids []OBLevelREST `json:"bids"`
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

// GetOrderBookSnapshot возвращает полный снапшот стакана на N уровней —
// "базу", от которой дальше применяются инкрементальные WS-дельты
// (см. orderbook.go). Публичный endpoint — авторизация не нужна.
//
// Endpoint: GET /futures/usdt/order_book?contract={symbol}&limit={depth}&with_id=true
// with_id=true обязателен — без него ответ не будет содержать поле id,
// а без id нельзя состыковать снапшот с потоком WS-дельт (см. официальный
// алгоритм ресинхронизации Gate.io: U <= id+1 <= u).
func (c *Client) GetOrderBookSnapshot(ctx context.Context, symbol string, depth int) (*OrderBookSnapshot, error) {
	var snapshot OrderBookSnapshot

	query := fmt.Sprintf("contract=%s&limit=%d&with_id=true", symbol, depth)
	err := c.GetPublic(ctx, "/futures/usdt/order_book", query, &snapshot)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения снапшота стакана %s: %w", symbol, err)
	}

	return &snapshot, nil
}
