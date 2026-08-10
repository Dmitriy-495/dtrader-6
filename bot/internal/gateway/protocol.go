// Этот файл описывает ФОРМАТ данных протокола Gate.io Futures WebSocket.
// Здесь только структуры (типы данных) — никакой логики, никаких функций
// с поведением. Если нужно понять "как выглядит сделка от биржи" —
// смотри сюда, а не в connection.go или parser.go.
package gateway

import "encoding/json"

// =============================================================================
// КОНВЕРТ СООБЩЕНИЯ (обёртка, в которую упаковано ЛЮБОЕ сообщение WS)
// =============================================================================

// WSRequest — то, что МЫ отправляем на Gate.io (подписки, ping).
type WSRequest struct {
	Time    int64    `json:"time"`
	Channel string   `json:"channel"`
	Event   string   `json:"event,omitempty"`
	Payload []string `json:"payload,omitempty"`
}

// WSResponse — то, что Gate.io присылает НАМ в ответ.
// Result — это json.RawMessage, то есть "сырые" JSON-байты, ещё не
// разобранные в конкретную структуру. Мы не знаем заранее, Trade там
// лежит, Candle или ContractStats — тип уже определяется по полю Channel.
// Поэтому сначала распаковываем WSResponse целиком, а Result парсим
// отдельно, зная канал (эта логика будет жить в parser.go).
type WSResponse struct {
	Time    int64           `json:"time"`
	Channel string          `json:"channel"`
	Event   string          `json:"event,omitempty"`
	Error   *WSError        `json:"error,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// WSError — структура ошибки, которую Gate.io кладёт в поле Error,
// если что-то пошло не так (неверная подписка, лимит и т.д.).
type WSError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// =============================================================================
// РЫНОЧНЫЕ ДАННЫЕ (то, что лежит внутри WSResponse.Result по каждому каналу)
// =============================================================================

// Trade — одна сделка на бирже (канал futures.trades).
// Size > 0 — taker купил (агрессивная покупка).
// Size < 0 — taker продал (агрессивная продажа).
type Trade struct {
	ID           int64  `json:"id"`
	Contract     string `json:"contract"`
	Size         string `json:"size"`
	Price        string `json:"price"`
	CreateTime   int64  `json:"create_time"`
	CreateTimeMs int64  `json:"create_time_ms"`
	// IsInternal — служебные сделки биржи (например, авто-делевередж).
	// Такие сделки мы не публикуем в Redis — это не реальный рыночный поток.
	IsInternal bool `json:"is_internal"`
}

// OBLevel — один уровень стакана: цена + объём на этой цене.
// "p" и "s" — сокращения от Gate.io (price, size), не мы их придумали.
type OBLevel struct {
	Price string `json:"p"`
	Size  string `json:"s"`
}

// OrderBookUpdate — обновление стакана с канала futures.order_book_update.
// Full=true означает, что это ПОЛНЫЙ снапшот на текущий момент (биржа
// иногда шлёт полные пересылки, не только инкрементальные дельты) —
// в этом случае нужно ЗАМЕНИТЬ локальный стакан целиком, а не применять
// как дельту поверх существующего состояния. Full=false (обычный случай)
// значит "вот что изменилось с прошлого раза" — применяем поверх.
type OrderBookUpdate struct {
	T      int64     `json:"t"`    // timestamp обновления (ms)
	Full   bool      `json:"full"` // true = полный снапшот, false/omitted = инкрементальная дельта
	S      string    `json:"s"`    // symbol, например "BTC_USDT"
	U      int64     `json:"u"`    // ID последнего обновления в этом пакете (last update ID)
	FirstU int64     `json:"U"`    // ID первого обновления в этом пакете (first update ID)
	Bids   []OBLevel `json:"b"`    // покупки (bid)
	Asks   []OBLevel `json:"a"`    // продажи (ask)
}

// OrderBookSnapshot — см. определение и комментарии в rest.go, рядом с
// методом GetOrderBookSnapshot, который его и возвращает. Здесь не
// дублируем — REST-специфичные типы (ответы конкретных эндпоинтов)
// естественнее держать рядом с методом, который их разбирает, а не
// среди WS-структур протокола (см. общий принцип этого файла в шапке).

// Candle — одна свеча (канал futures.candlesticks).
// Window=true означает "эта свеча уже ЗАКРЫЛАСЬ" — только такие
// свечи имеет смысл сохранять в Redis, иначе будем писать
// недостроенную свечу на каждое обновление внутри минуты.
type Candle struct {
	T      int64  `json:"t"` // timestamp открытия свечи
	Open   string `json:"o"`
	Close  string `json:"c"`
	High   string `json:"h"`
	Low    string `json:"l"`
	Volume string `json:"v"`
	// Name — приходит от биржи в формате "1m_BTC_USDT" (таймфрейм + символ
	// через подчёркивание). Извлечение чистого символа "BTC_USDT" —
	// это уже логика парсинга, она будет жить в parser.go, а не здесь.
	Name   string `json:"n"`
	Amount string `json:"a"`
	Window bool   `json:"w"`
}

// Liquidation — одна принудительная ликвидация позиции (канал futures.public_liquidates).
// Size > 0 — ликвидирован лонг. Size < 0 — ликвидирован шорт.
type Liquidation struct {
	Price    string `json:"price"`
	Size     string `json:"size"`
	TimeMs   int64  `json:"time"`
	Contract string `json:"contract"`
}

// ContractStats — статистика контракта раз в минуту (канал futures.contract_stats).
// Именно отсюда TUI берёт LSR (long/short ratio) и Open Interest.
//
// Почему тут json.Number, а не float64 или string?
// json.Number — это строка ПОД КАПОТОМ, но с встроенной возможностью
// вызвать .Float64() или .Int64() когда понадобится посчитать. Gate.io
// может прислать число и как "1.25", и как 1.25 (без кавычек) в разных
// ситуациях — json.Number одинаково хорошо разбирает оба варианта,
// а обычный float64 бы упал на JSON-строке, а обычный string — на
// JSON-числе без кавычек.
type ContractStats struct {
	Time            int64       `json:"time"`
	Contract        string      `json:"contract"`
	OpenInterest    json.Number `json:"open_interest"`
	OpenInterestUSD json.Number `json:"open_interest_usd"`
	LsrTaker        json.Number `json:"lsr_taker"`
	LsrAccount      json.Number `json:"lsr_account"`
	LongLiqSize     json.Number `json:"long_liq_size"`
	ShortLiqSize    json.Number `json:"short_liq_size"`
	LongLiqUSD      json.Number `json:"long_liq_usd"`
	ShortLiqUSD     json.Number `json:"short_liq_usd"`
	TopLsrAccount   json.Number `json:"top_lsr_account"`
	TopLsrSize      json.Number `json:"top_lsr_size"`
	MarkPrice       json.Number `json:"mark_price"`
}
