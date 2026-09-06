// Пакет publisher — единственная точка записи signal-engine в Redis,
// по прямой аналогии с analyzer/internal/publisher: остальной код
// никогда не вызывает rdb.Set напрямую, только через методы этого
// пакета — формат ключей и TTL описаны в одном месте.
//
// ВАЖНО: signals:{symbol} — это ТОЛЬКО опубликованное решение
// signal-engine (LONG/SHORT/HOLD + причина). Публикация сюда сама по
// себе НЕ отправляет ордера на биржу — signal-engine согласован как
// read-only относительно Gate.io, ровно как bot и analyzer. Исполнение
// ордеров (сервис B "executor" в таблице CHECKPOINT.md) — отдельный,
// ещё не построенный сервис, и подключать его к боевым ордерам можно
// только после явного отдельного решения автора (см. вопрос "готов ли
// автор сначала тестировать сигналы в режиме только-логировать" в
// PROMPT_NEXT.md).
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Dmitriy-495/dtrader-6/signal-engine/internal/rules"
)

// signalTTL — TTL для ключей signals:*. Тот же принцип, что и
// indicatorTTL в analyzer/internal/publisher: если signal-engine упал,
// последний сигнал должен "протухнуть" сам, а не остаться в Redis
// как будто бы актуальное решение навсегда.
//
// Выбрано короче, чем indicatorTTL (60s) в analyzer, потому что сигнал
// зависит от свежести ВХОДНЫХ данных (indicators:*, которые сами
// протухают за 60s) — сигнал не может быть "свежее" своих входов.
const signalTTL = 30 * time.Second

// SignalRecord — то, что публикуется в signals:{symbol}. Помимо самого
// решения (Type/Reason из rules.Signal) содержит Symbol и Ts — так
// потребитель (TUI, лог, будущий risk-guard) может отличить один
// сигнал от другого, не разбирая имя ключа Redis, из которого он
// пришёл.
type SignalRecord struct {
	Symbol string            `json:"symbol"`
	Type   rules.SignalType  `json:"type"`
	Reason rules.Reason      `json:"reason"`
	Ts     int64             `json:"ts"`
}

// Publisher пишет решения signal-engine в Redis.
type Publisher struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// PublishSignal записывает решение для одного символа в
// signals:{symbol}. ts — unix ms момента принятия решения, передаётся
// снаружи (не time.Now() внутри) по тому же принципу, что и в
// analyzer/internal/indicator: чистая точка входа без скрытого
// обращения к системным часам облегчает тестирование.
func (p *Publisher) PublishSignal(ctx context.Context, symbol string, signal rules.Signal, ts int64) error {
	key := fmt.Sprintf("signals:%s", symbol)
	record := SignalRecord{
		Symbol: symbol,
		Type:   signal.Type,
		Reason: signal.Reason,
		Ts:     ts,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("PublishSignal marshal %s: %w", symbol, err)
	}
	if err := p.rdb.Set(ctx, key, raw, signalTTL).Err(); err != nil {
		return fmt.Errorf("PublishSignal %s: %w", symbol, err)
	}
	return nil
}
