package reader

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Trade — одна сделка, прочитанная из market:trades:{symbol} (Stream).
// Size сохраняет знак ровно как публикует bot (см. gateway/parser.go
// handleTrades: "size" пишется как есть из Gate.io) — Size>0 значит
// агрессивная покупка (taker купил), Size<0 значит агрессивная продажа.
// Это тот же контракт, на который уже полагается ws-server/reader/redis.go
// (readTrades: "if size > 0 { BuyVol += size } else { SellVol += -size }").
type Trade struct {
	Price float64
	Size  float64
	TsMs  int64
}

// TradeReader читает поток сделок из Redis Stream через блокирующий
// XREAD — см. согласованное решение: trades читаются через Stream (а не
// poll), потому что для V важен КАЖДЫЙ тик и его порядок, а не текущий
// снапшот. Реализация читает по тому же принципу, что и readTrades в
// ws-server/internal/reader/redis.go — тот же Redis, тот же тип ключа,
// тот же способ чтения, отличается только что делается с прочитанными
// данными дальше (там — агрегация для WS-клиентов, здесь — накопление
// для расчёта V).
type TradeReader struct {
	rdb *redis.Client
}

func NewTradeReader(rdb *redis.Client) *TradeReader {
	return &TradeReader{rdb: rdb}
}

// Run запускает блокирующий цикл чтения market:trades:{symbol} и
// вызывает onTrade для каждой прочитанной сделки. Возвращается только
// когда ctx отменён (graceful shutdown) — вызывающий код (engine/) должен
// запускать Run в отдельной горутине.
//
// lastID="$" означает "начать слушать с сообщений, пришедших ПОСЛЕ
// запуска" — то же поведение, что и в ws-server (см. readTrades: lastID
// := "$"). Analyzer не пытается обработать всю историю Stream при
// старте: буфер для V (окно последней минуты/8м/24м) всё равно
// накапливается заново на живых данных, исторические тики не нужны.
func (r *TradeReader) Run(ctx context.Context, symbol string, onTrade func(Trade)) {
	key := fmt.Sprintf("market:trades:%s", symbol)
	lastID := "$"

	for {
		if ctx.Err() != nil {
			return
		}
		streams, err := r.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{key, lastID},
			Count:   200,
			Block:   5 * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("⚠️ reader trades %s: %v", symbol, err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				trade, err := parseTradeFields(msg.Values)
				if err != nil {
					log.Printf("⚠️ reader trades %s: пропущена запись: %v", symbol, err)
					continue
				}
				onTrade(trade)
			}
		}
	}
}

// parseTradeFields разбирает поля XADD-сообщения (map[string]interface{}
// от go-redis — значения возвращаются как string независимо от того,
// что писалось при XAdd, это особенность протокола Redis Stream) в
// строгую структуру Trade.
func parseTradeFields(values map[string]interface{}) (Trade, error) {
	priceStr := fmt.Sprintf("%v", values["price"])
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return Trade{}, fmt.Errorf("price %q: %w", priceStr, err)
	}

	sizeStr := fmt.Sprintf("%v", values["size"])
	size, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return Trade{}, fmt.Errorf("size %q: %w", sizeStr, err)
	}

	tsStr := fmt.Sprintf("%v", values["ts"])
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return Trade{}, fmt.Errorf("ts %q: %w", tsStr, err)
	}

	return Trade{Price: price, Size: size, TsMs: ts}, nil
}
