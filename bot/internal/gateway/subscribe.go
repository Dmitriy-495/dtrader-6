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
