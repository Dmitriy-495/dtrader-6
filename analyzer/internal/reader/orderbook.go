package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/indicator"
)

// RawOBLevel — один уровень стакана ровно в JSON-формате Gate.io/bot:
// поля "p" (price) и "s" (size), см. gateway.OBLevel в bot.
type RawOBLevel struct {
	Price string `json:"p"`
	Size  string `json:"s"`
}

// RawOrderBook — снимок стакана ровно в том виде, в котором его
// публикует bot в market:orderbook:{symbol} (см. gateway.OrderBookFullSnapshot).
//
// С 2026-08-07 (см. CHECKPOINT.md, раздел 13b) bot публикует сюда
// ПОЛНЫЙ, поддерживаемый в памяти стакан (REST-снапшот + применённые
// поверх него WS-дельты, с отслеживанием разрывов последовательности
// и авто-пересинхронизацией) — не последнюю сырую дельту, как было
// раньше. Формат полей не менялся (s/b/a, p/s внутри уровня), поэтому
// в этом файле по факту ничего менять не потребовалось — ровно как и
// предполагалось при исходном проектировании.
type RawOrderBook struct {
	S    string       `json:"s"`
	Bids []RawOBLevel `json:"b"`
	Asks []RawOBLevel `json:"a"`
}

// OrderBookReader читает market:orderbook:{symbol} (String, JSON) через
// периодический опрос — согласованное решение: это снапшот состояния,
// а не поток событий, poll подходит лучше XREAD (там и не Stream).
type OrderBookReader struct {
	rdb *redis.Client
}

func NewOrderBookReader(rdb *redis.Client) *OrderBookReader {
	return &OrderBookReader{rdb: rdb}
}

// Fetch читает текущий стакан символа и возвращает срезы уровней bid/ask,
// обрезанные до depth уровней (ближайшие к цене — то есть с начала среза,
// т.к. Gate.io присылает уровни уже отсортированными от лучшей цены).
// Если данных в Redis ещё нет (ключ не существует — bot только
// запустился или ещё не публиковал стакан для этого символа), возвращает
// пустые срезы и nil error — отсутствие стакана это не ошибка чтения, а
// нормальное переходное состояние на старте.
func (r *OrderBookReader) Fetch(ctx context.Context, symbol string, depth int) (bids, asks []indicator.OBLevel, err error) {
	key := fmt.Sprintf("market:orderbook:%s", symbol)
	val, err := r.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("Get %s: %w", key, err)
	}

	var raw RawOrderBook
	if err := json.Unmarshal([]byte(val), &raw); err != nil {
		return nil, nil, fmt.Errorf("unmarshal orderbook %s: %w", symbol, err)
	}

	bids, err = parseLevels(raw.Bids, depth)
	if err != nil {
		return nil, nil, fmt.Errorf("bids %s: %w", symbol, err)
	}
	asks, err = parseLevels(raw.Asks, depth)
	if err != nil {
		return nil, nil, fmt.Errorf("asks %s: %w", symbol, err)
	}
	return bids, asks, nil
}

func parseLevels(raw []RawOBLevel, depth int) ([]indicator.OBLevel, error) {
	limit := len(raw)
	if depth > 0 && depth < limit {
		limit = depth
	}

	levels := make([]indicator.OBLevel, 0, limit)
	for i := 0; i < limit; i++ {
		price, err := strconv.ParseFloat(raw[i].Price, 64)
		if err != nil {
			return nil, fmt.Errorf("price %q: %w", raw[i].Price, err)
		}
		size, err := strconv.ParseFloat(raw[i].Size, 64)
		if err != nil {
			return nil, fmt.Errorf("size %q: %w", raw[i].Size, err)
		}
		levels = append(levels, indicator.OBLevel{Price: price, Size: size})
	}
	return levels, nil
}
