package gateway

import (
	"fmt"
	"log"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

func (c *WSClient) SubscribeTickers(symbols []string) error {
	msg := WSRequest{
		Time:    utils.NowUnix(),
		Channel: "futures.tickers",
		Event:   "subscribe",
		Payload: symbols,
	}
	if err := c.writeJSON(msg); err != nil {
		return fmt.Errorf("subscribe tickers: %w", err)
	}
	log.Printf("📈 [tickers] подписка отправлена: %v", symbols)
	return nil
}

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
			// Три элемента: символ, глубина, интервал
			// "20"  — глубина стакана (20 уровней bid + 20 ask)
			// "0"   — интервал: 0 = каждое изменение без задержки
			Payload: []string{symbol, "20", "0"},
		}
		if err := c.writeJSON(msg); err != nil {
			return fmt.Errorf("subscribe order_book %s: %w", symbol, err)
		}
		log.Printf("📖 [order_book] подписка отправлена: %s depth=20", symbol)
	}
	return nil
}
