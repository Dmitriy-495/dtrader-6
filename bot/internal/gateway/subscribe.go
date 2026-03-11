package gateway

import (
	"fmt"
	"log"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

// SubscribeTrades — сделки по символам
// size > 0 = taker покупатель, size < 0 = taker продавец
func (c *WSClient) SubscribeTrades(symbols []string) error {
	msg := WSRequest{
		Time:    utils.NowUnix(),
		Channel: "futures.trades",
		Event:   "subscribe",
		Payload: symbols,
	}
	if err := c.writeJSON(msg); err != nil {
		return fmt.Errorf("subscribe trades: %w", err)
	}
	log.Printf("💹 [trades] подписка отправлена: %v", symbols)
	return nil
}

// SubscribeOrderBookUpdate — инкрементальный стакан 100ms глубина 20
// эффективнее чем order_book — шлёт только изменения, не весь стакан
// best bid/ask = первый уровень обновлённого стакана
func (c *WSClient) SubscribeOrderBookUpdate(symbols []string) error {
	for _, symbol := range symbols {
		msg := WSRequest{
			Time:    utils.NowUnix(),
			Channel: "futures.order_book_update",
			Event:   "subscribe",
			// symbol, частота обновления, глубина
			Payload: []string{symbol, "100ms", "20"},
		}
		if err := c.writeJSON(msg); err != nil {
			return fmt.Errorf("subscribe order_book_update %s: %w", symbol, err)
		}
		log.Printf("📖 [order_book_update] подписка отправлена: %s 100ms depth=20", symbol)
	}
	return nil
}

// SubscribeCandlesticks — свечи 1m
// w=true означает закрытие свечи — только тогда пишем в Redis
func (c *WSClient) SubscribeCandlesticks(symbols []string) error {
	for _, symbol := range symbols {
		msg := WSRequest{
			Time:    utils.NowUnix(),
			Channel: "futures.candlesticks",
			Event:   "subscribe",
			Payload: []string{"1m", symbol},
		}
		if err := c.writeJSON(msg); err != nil {
			return fmt.Errorf("subscribe candlesticks %s: %w", symbol, err)
		}
		log.Printf("🕯️ [candlesticks] подписка отправлена: %s 1m", symbol)
	}
	return nil
}

// SubscribePublicLiquidates — публичные ликвидации realtime
// size > 0 = ликвидация лонга, size < 0 = ликвидация шорта
func (c *WSClient) SubscribePublicLiquidates(symbols []string) error {
	msg := WSRequest{
		Time:    utils.NowUnix(),
		Channel: "futures.public_liquidates",
		Event:   "subscribe",
		Payload: symbols,
	}
	if err := c.writeJSON(msg); err != nil {
		return fmt.Errorf("subscribe public_liquidates: %w", err)
	}
	log.Printf("💥 [public_liquidates] подписка отправлена: %v", symbols)
	return nil
}

// SubscribeContractStats — статистика контракта каждую минуту:
// open_interest, lsr_taker, lsr_account, long/short_liq_size, top_lsr
func (c *WSClient) SubscribeContractStats(symbols []string) error {
	for _, symbol := range symbols {
		msg := WSRequest{
			Time:    utils.NowUnix(),
			Channel: "futures.contract_stats",
			Event:   "subscribe",
			Payload: []string{symbol, "1m"},
		}
		if err := c.writeJSON(msg); err != nil {
			return fmt.Errorf("subscribe contract_stats %s: %w", symbol, err)
		}
		log.Printf("📊 [contract_stats] подписка отправлена: %s 1m", symbol)
	}
	return nil
}
