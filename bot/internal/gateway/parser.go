// Этот файл отвечает ТОЛЬКО за разбор и обработку рыночных данных по
// каждому конкретному каналу Gate.io: как только ReadLoop (см. ws.go)
// определил, что за канал пришёл — управление передаётся сюда.
// Здесь нет чтения из сети (см. connection.go) и нет ping/pong (см. pingloop.go).
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// parseLiquidations разбирает поле Result канала futures.public_liquidates.
// Особенность протокола Gate.io: биржа присылает ТО массив ликвидаций,
// ТО одиночный объект — в зависимости от того, сколько ликвидаций
// произошло за тик. Поэтому сначала пробуем распарсить как массив,
// и только если не получилось — как одиночный объект.
func parseLiquidations(raw json.RawMessage) ([]Liquidation, error) {
	var liqs []Liquidation
	if err := json.Unmarshal(raw, &liqs); err == nil {
		return liqs, nil
	}
	var liq Liquidation
	if err := json.Unmarshal(raw, &liq); err == nil {
		return []Liquidation{liq}, nil
	}
	return nil, fmt.Errorf("не удалось распарсить ликвидацию")
}

// handleTrades обрабатывает пакет сделок с канала futures.trades.
// Внутренние (служебные) сделки биржи — например, авто-делевередж —
// пропускаем: это не реальный рыночный поток, публиковать их в Redis
// значит засорять данные, на которых потом считает analyzer.
func (c *WSClient) handleTrades(ctx context.Context, raw json.RawMessage) {
	var trades []Trade
	if err := json.Unmarshal(raw, &trades); err != nil {
		log.Printf("⚠️ trades parse error: %v", err)
		return
	}
	for _, t := range trades {
		if t.IsInternal {
			continue
		}
		if c.pub != nil {
			if err := c.pub.PublishTrade(ctx, t.Contract, map[string]interface{}{
				"id":    t.ID,
				"price": t.Price,
				"size":  t.Size,
				"ts":    t.CreateTimeMs,
			}); err != nil {
				log.Printf("⚠️ publish trade failed: symbol=%s err=%v", t.Contract, err)
				c.pub.Metrics.IncDropped()
			}
		}
	}
}

// handleOrderBook обрабатывает инкрементальное обновление стакана
// с канала futures.order_book_update. В отличие от trades/candles,
// здесь один объект на сообщение, а не массив — публикуем как есть.
func (c *WSClient) handleOrderBook(ctx context.Context, raw json.RawMessage) {
	var ob OrderBookUpdate
	if err := json.Unmarshal(raw, &ob); err != nil {
		log.Printf("⚠️ order_book_update parse error: %v", err)
		return
	}
	if c.pub != nil {
		if err := c.pub.PublishOrderBook(ctx, ob.S, ob); err != nil {
			log.Printf("⚠️ publish order_book failed: symbol=%s err=%v", ob.S, err)
			c.pub.Metrics.IncDropped()
		}
	}
}

// handleCandles обрабатывает пакет свечей с канала futures.candlesticks.
// Публикуем только ЗАКРЫТЫЕ свечи (candle.Window == true) — иначе на
// каждое промежуточное обновление внутри текущей минуты мы бы писали
// в Redis недостроенную свечу, и analyzer считал бы по неполным данным.
func (c *WSClient) handleCandles(ctx context.Context, raw json.RawMessage) {
	var candles []Candle
	if err := json.Unmarshal(raw, &candles); err != nil {
		log.Printf("⚠️ candlesticks parse error: %v", err)
		return
	}
	for _, candle := range candles {
		if candle.Window && c.pub != nil {
			// Gate.io шлёт Name в формате "1m_BTC_USDT" (таймфрейм
			// + символ через подчёркивание). Первые 3 символа ("1m_")
			// отрезаем, чтобы получить чистый символ "BTC_USDT".
			symbol := candle.Name
			if len(symbol) > 3 {
				symbol = symbol[3:]
			}
			if err := c.pub.PublishCandle(ctx, symbol, candle); err != nil {
				log.Printf("⚠️ publish candle failed: symbol=%s err=%v", symbol, err)
				c.pub.Metrics.IncDropped()
			}
		}
	}
}

// handleLiquidations обрабатывает ликвидации с канала futures.public_liquidates.
func (c *WSClient) handleLiquidations(ctx context.Context, raw json.RawMessage) {
	liqs, err := parseLiquidations(raw)
	if err != nil {
		log.Printf("⚠️ liquidates parse error: %v", err)
		return
	}
	for _, liq := range liqs {
		if c.pub != nil {
			if err := c.pub.PublishLiquidation(ctx, liq.Contract, map[string]interface{}{
				"price":   liq.Price,
				"size":    liq.Size,
				"time_ms": liq.TimeMs,
			}); err != nil {
				log.Printf("⚠️ publish liquidation failed: symbol=%s err=%v", liq.Contract, err)
				c.pub.Metrics.IncDropped()
			}
		}
	}
}

// handleContractStats обрабатывает статистику контракта с канала
// futures.contract_stats (OI, LSR и т.д. — раз в минуту).
func (c *WSClient) handleContractStats(ctx context.Context, raw json.RawMessage) {
	var stats ContractStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		log.Printf("⚠️ contract_stats parse error: %v", err)
		return
	}
	if c.pub != nil {
		if err := c.pub.PublishContractStats(ctx, stats.Contract, stats); err != nil {
			log.Printf("⚠️ publish contract_stats failed: symbol=%s err=%v", stats.Contract, err)
			c.pub.Metrics.IncDropped()
		}
	}
}
