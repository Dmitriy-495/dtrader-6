package gateway

import (
	"fmt"
	"log"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

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

func (c *WSClient) SubscribeOrderBook(symbols []string) error {
	for _, symbol := range symbols {
		msg := WSRequest{
			Time:    utils.NowUnix(),
			Channel: "futures.order_book",
			Event:   "subscribe",
			Payload: []string{symbol, "20", "0"},
		}
		if err := c.writeJSON(msg); err != nil {
			return fmt.Errorf("subscribe order_book %s: %w", symbol, err)
		}
		log.Printf("📖 [order_book] подписка отправлена: %s depth=20", symbol)
	}
	return nil
}

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
