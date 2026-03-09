package gateway

import (
	"fmt"
	"log"

	"github.com/Dmitriy-495/dtrader-6/bot/internal/utils"
)

func (c *WSClient) SubscribeAll(symbols []string) error {
	if err := c.subscribeCandlesticks(symbols); err != nil {
		return fmt.Errorf("ошибка подписки на свечи: %w", err)
	}
	if err := c.subscribeTrades(symbols); err != nil {
		return fmt.Errorf("ошибка подписки на тики: %w", err)
	}
	if err := c.subscribeOrderBook(symbols); err != nil {
		return fmt.Errorf("ошибка подписки на стакан: %w", err)
	}
	return nil
}

func (c *WSClient) subscribeCandlesticks(symbols []string) error {
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
		log.Printf("📊 [candlesticks] подписка отправлена: %s 1m", symbol)
	}
	return nil
}

func (c *WSClient) subscribeTrades(symbols []string) error {
	msg := WSRequest{
		Time:    utils.NowUnix(),
		Channel: "futures.trades",
		Event:   "subscribe",
		Payload: symbols,
	}
	if err := c.writeJSON(msg); err != nil {
		return fmt.Errorf("subscribe trades: %w", err)
	}
	for _, s := range symbols {
		log.Printf("💹 [trades] подписка отправлена: %s", s)
	}
	return nil
}

func (c *WSClient) subscribeOrderBook(symbols []string) error {
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
