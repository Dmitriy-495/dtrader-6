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
	"strings"
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
	err := json.Unmarshal(raw, &liq)
	if err == nil {
		return []Liquidation{liq}, nil
	}
	// Оба варианта парсинга не сработали — оборачиваем именно ошибку
	// разбора как одиночный объект (%w сохраняет её как причину для
	// errors.Is/errors.As), это более информативный вариант из двух:
	// массив редко имеет смысл присылать пустым или единственным
	// элементом, поэтому чаще всего реальная проблема протокола
	// обнаруживается именно на попытке разбора как объекта.
	return nil, fmt.Errorf("не удалось распарсить ликвидацию (ни как массив, ни как объект): %w", err)
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

// handleOrderBook обрабатывает входящее сообщение с канала
// futures.order_book_update — это может быть либо ПОЛНЫЙ снапшот
// (Full == true, редкий случай — см. protocol.go и обработку в
// orderbook.go/ApplyDelta), либо, в подавляющем большинстве случаев,
// ИНКРЕМЕНТАЛЬНАЯ дельта. Разница обрабатывается внутри
// LocalOrderBook.ApplyDelta — здесь эта деталь не важна, просто
// передаём сообщение как есть.
//
// В отличие от предыдущей версии (которая публиковала сырую дельту
// как есть — см. CHECKPOINT.md, раздел 13b), эта версия:
//  1. применяет дельту к локально поддерживаемому полному стакану
//     (см. orderbook.go, LocalOrderBook.ApplyDelta)
//  2. публикует в Redis уже ПОЛНЫЙ стакан после применения — analyzer
//     спроектирован читать из market:orderbook:{symbol} именно полный
//     снапшот, не дельту (см. CHECKPOINT.md, раздел 13a)
//  3. при обнаружении разрыва последовательности запускает
//     пересинхронизацию в отдельной горутине — не блокирует ReadLoop
//     на время REST-запроса
func (c *WSClient) handleOrderBook(ctx context.Context, raw json.RawMessage) {
	var ob OrderBookUpdate
	if err := json.Unmarshal(raw, &ob); err != nil {
		log.Printf("⚠️ order_book_update parse error: %v", err)
		return
	}

	c.booksMu.Lock()
	lob, exists := c.books[ob.S]
	c.booksMu.Unlock()

	if !exists {
		// Дельта пришла раньше, чем успел отработать InitOrderBookSnapshots
		// (см. main.go — снапшоты запрашиваются ДО подписки на канал,
		// но сетевые вызовы не мгновенны). Это ожидаемая гонка на старте,
		// не ошибка — просто пропускаем дельту и ждём следующую, снапшот
		// появится очень скоро.
		return
	}

	applied, needResync := lob.ApplyDelta(ob)

	if needResync {
		log.Printf("🔄 [orderbook] обнаружен разрыв последовательности: %s — пересинхронизация", ob.S)
		// depth берём из lob.Depth() — реальной глубины, с которой был
		// запрошен уже загруженный снапшот (сохранена в LocalOrderBook
		// при его создании) — столько уровней запросили изначально,
		// столько и запрашиваем заново, глубина не должна "плавать"
		// между вызовами (см. предупреждение в официальной документации
		// Gate.io о необходимости совпадения depth снапшота и level
		// подписки).
		//
		// НЕ путать с len(ob.Bids)/len(ob.Asks) — это длина ТЕКУЩЕЙ
		// ВХОДЯЩЕЙ ДЕЛЬТЫ (обычно всего несколько изменившихся уровней,
		// не полная глубина стакана). Именно так этот код был написан
		// раньше и содержал баг — найдено независимым аудитом
		// (OpenCode + Claude Sonnet 5, 2026-08-10): подмена переменных
		// ob (дельта) и lob (загруженный стакан) с похожими именами.
		depth := lob.Depth()

		if !c.tryStartResync(ob.S) {
			// Resync для этого символа уже идёт — не запускаем ещё один
			// параллельный REST-запрос (см. tryStartResync).
			return
		}
		go c.resyncOrderBook(ob.S, depth)
		return
	}

	if !applied {
		// Ждём точку стыковки со свежим снапшотом — см. комментарий
		// в LocalOrderBook.ApplyDelta, это ожидаемо в первые мгновения
		// после инициализации, не ошибка.
		return
	}

	if c.pub != nil {
		snapshot := lob.Snapshot(ob.T)
		if err := c.pub.PublishOrderBook(ctx, ob.S, snapshot); err != nil {
			log.Printf("⚠️ publish order_book failed: symbol=%s err=%v", ob.S, err)
			c.pub.Metrics.IncDropped()
		}
	}
}

// parseSymbolFromCandleName извлекает символ из поля Name канала
// futures.candlesticks. Gate.io шлёт Name в формате
// "{timeframe}_{symbol}", например "1m_BTC_USDT" — отрезаем префикс
// таймфрейма до первого "_". Если разделитель не найден — возвращаем
// name как есть (защита от неожиданного формата, лучше опубликовать
// под странным, но не пустым символом, чем молча потерять данные).
//
// Вынесена в отдельную функцию (не инлайн внутри handleCandles) именно
// для тестируемости — раньше здесь был захардкоженный name[3:]
// (предполагал ровно 3 символа префикса, как у "1m_"), который молча
// ломался бы для таймфреймов с более длинным префиксом ("15m_", "30m_").
// Найдено независимым аудитом (OpenCode + Claude Sonnet 5, 2026-08-10).
// Сейчас bot подписывается только на 1m (см. SubscribeCandlesticks в
// subscribe.go), поэтому баг не проявлялся на практике, но разбор по
// разделителю устойчив к префиксу любой длины на будущее.
func parseSymbolFromCandleName(name string) string {
	if idx := strings.IndexByte(name, '_'); idx >= 0 {
		return name[idx+1:]
	}
	return name
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
			symbol := parseSymbolFromCandleName(candle.Name)
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