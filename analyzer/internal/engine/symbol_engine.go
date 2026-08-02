// Пакет engine — это "верхний уровень" analyzer: один Engine на символ,
// который владеет собственным состоянием и независимым циклом (согласно
// принятому решению "горутина на символ, независимый цикл", по аналогии
// с parser.go в bot). Engine связывает reader/ (чтение market:*),
// indicator/ (чистая математика) и publisher/ (запись indicators:*) —
// сам не содержит ни Redis-протокола, ни формул технического анализа.
package engine

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/config"
	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/indicator"
	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/publisher"
	"github.com/Dmitriy-495/dtrader-6/analyzer/internal/reader"
)

// tradeWindow — накопленный за текущее, ещё не закрытое окно объём
// покупок/продаж по одному таймфрейму. Отдельная копия на каждый
// сконфигурированный ТФ (1m/8m/24m) — потому что V считается за разные
// по длине окна на разных ТФ одновременно, из одного и того же потока
// сделок.
type tradeWindow struct {
	buyVol       float64
	sellVol      float64
	windowStart  time.Time
	windowLength time.Duration

	// recentTotals — суммарные объёмы (buy+sell) предыдущих ЗАКРЫТЫХ
	// окон этого ТФ, используется для детекции Volume Spike в
	// indicator.CalcVolume (см. VolumeConfig.SMAPeriod). Ограничен
	// длиной SMAPeriod — старые значения вытесняются новыми.
	recentTotals []float64
	smaPeriod    int
}

// rollIfExpired проверяет, истекло ли текущее окно, и если да — сдвигает
// его: текущие buy/sell попадают в recentTotals как ЗАКРЫТОЕ окно, а
// новое окно начинается с нуля. now передаётся параметром (а не берётся
// через time.Now() внутри), чтобы весь Engine ориентировался на единое
// "сейчас" за один цикл calcTicker — не самый принципиальный момент здесь,
// но избавляет от рассинхрона между несколькими окнами разных ТФ в
// пределах одного тика.
func (w *tradeWindow) rollIfExpired(now time.Time) {
	if now.Sub(w.windowStart) < w.windowLength {
		return
	}
	total := w.buyVol + w.sellVol
	w.recentTotals = append(w.recentTotals, total)
	if len(w.recentTotals) > w.smaPeriod {
		w.recentTotals = w.recentTotals[len(w.recentTotals)-w.smaPeriod:]
	}
	w.buyVol = 0
	w.sellVol = 0
	w.windowStart = now
}

// symbolState — состояние ОДНОГО символа, разделяемое между тремя
// читателями (candles/trades/orderbook, пишут независимо) и calcTicker
// (читает всё разом раз в calc_interval). Единственный mu защищает и
// trade-окна, и последний снапшот стакана — отдельные свечи (candles)
// намеренно читаются заново из Redis на каждый тик calcTicker, а не
// кэшируются здесь, потому что market:candles:1m и так уже хранит
// нужную историю (см. reader.CandleReader.FetchRecent1m) — дублировать
// её в памяти Engine нет смысла, в отличие от trades/orderbook, где
// накопление между тиками обязательно (иначе изменения между тиками
// потерялись бы).
type symbolState struct {
	mu sync.Mutex

	// tradeWindows — по одному tradeWindow на каждый сконфигурированный
	// таймфрейм (ключ — имя ТФ, "1m"/"8m"/"24m").
	tradeWindows map[string]*tradeWindow

	lastBids []indicator.OBLevel
	lastAsks []indicator.OBLevel
}

// Engine считает T/V/P для ОДНОГО символа и публикует результат в Redis.
// Создаётся по одному экземпляру на каждый символ из cfg.Symbols —
// см. Run в cmd/main.go, где эти экземпляры запускаются в отдельных
// горутинах.
type Engine struct {
	symbol string
	cfg    *config.Config

	candleReader *reader.CandleReader
	tradeReader  *reader.TradeReader
	obReader     *reader.OrderBookReader
	pub          *publisher.Publisher

	state *symbolState
}

// New создаёт Engine для одного символа. rdb передаётся уже готовым
// подключением (общим на все символы — см. redisclient.New в main.go),
// а не создаётся здесь заново: одно соединение с пулом эффективнее
// множества отдельных клиентов на каждый символ.
func New(symbol string, cfg *config.Config, rdb *redis.Client) *Engine {
	windows := make(map[string]*tradeWindow)
	allTFs := append([]string{cfg.Timeframes.Base}, tfNames(cfg.Timeframes.Aggregates)...)
	for _, tf := range allTFs {
		windows[tf] = &tradeWindow{
			windowStart:  time.Now(),
			windowLength: tfDuration(tf, cfg),
			smaPeriod:    cfg.Indicators.Volume.SMAPeriod,
		}
	}

	return &Engine{
		symbol:       symbol,
		cfg:          cfg,
		candleReader: reader.NewCandleReader(rdb),
		tradeReader:  reader.NewTradeReader(rdb),
		obReader:     reader.NewOrderBookReader(rdb),
		pub:          publisher.New(rdb),
		state:        &symbolState{tradeWindows: windows},
	}
}

// Run запускает четыре горутины Engine (readTrades, pollOrderBook,
// calcTicker — согласно принятому решению "горутина на символ,
// независимый цикл") и блокируется до отмены ctx. candles НЕ читаются
// в отдельной постоянной горутине — они читаются напрямую внутри
// calcTicker на каждый тик (см. комментарий у symbolState выше, почему
// это не требует отдельного накопления состояния).
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		e.tradeReader.Run(ctx, e.symbol, e.onTrade)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		e.pollOrderBook(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		e.calcTicker(ctx)
	}()

	wg.Wait()
}

// onTrade — колбэк для TradeReader.Run: распределяет одну сделку по
// всем trade-окнам символа (1m/8m/24m считаются из одного и того же
// потока сделок параллельно, каждое окно накапливает свою сумму
// независимо).
func (e *Engine) onTrade(t reader.Trade) {
	now := time.Now()
	e.state.mu.Lock()
	defer e.state.mu.Unlock()

	for _, w := range e.state.tradeWindows {
		w.rollIfExpired(now)
		if t.Size > 0 {
			w.buyVol += t.Size
		} else {
			w.sellVol += -t.Size
		}
	}
}

// pollOrderBook периодически (раз в 1s — глубина стакана меняется
// быстро, но P пересчитывается тикером раз в calc_interval, поэтому
// частый poll здесь лишь поддерживает state.lastBids/lastAsks свежими
// между тиками расчёта) читает market:orderbook:{symbol} и сохраняет
// последний прочитанный снапшот в state.
func (e *Engine) pollOrderBook(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bids, asks, err := e.obReader.Fetch(ctx, e.symbol, e.cfg.Indicators.Pressure.Depth)
			if err != nil {
				log.Printf("⚠️ engine %s: чтение orderbook: %v", e.symbol, err)
				continue
			}
			e.state.mu.Lock()
			e.state.lastBids = bids
			e.state.lastAsks = asks
			e.state.mu.Unlock()
		}
	}
}

// calcTicker — раз в cfg.CalcIntervalDuration() считает T (по всем
// таймфреймам), V (по всем таймфреймам) и P, публикует результаты через
// publisher. Это единственное место, где reader/indicator/publisher
// встречаются вместе для расчёта — сами reader и indicator друг про
// друга не знают.
func (e *Engine) calcTicker(ctx context.Context) {
	interval := e.cfg.CalcIntervalDuration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.calcOnce(ctx)
		}
	}
}

// calcOnce выполняет один цикл расчёта T/V/P для символа. Вынесена из
// calcTicker отдельной функцией ради тестируемости (можно вызвать
// напрямую в тесте с контролируемым ctx, не дожидаясь реального тикера).
func (e *Engine) calcOnce(ctx context.Context) {
	now := reader.NowMs()

	e.calcTrendAndVolume(ctx, now)
	e.calcPressure(ctx, now)
}

func (e *Engine) calcTrendAndVolume(ctx context.Context, nowMs int64) {
	allTFs := append([]string{e.cfg.Timeframes.Base}, tfNames(e.cfg.Timeframes.Aggregates)...)

	// Читаем достаточно 1m-свечей, чтобы после агрегации в САМЫЙ КРУПНЫЙ
	// настроенный ТФ (обычно 24m) осталось хватало точек для его периодов
	// (EMA slow, RSI, Angle). Берём с запасом: maxMinutes(ТФ) умножить
	// на (нужное число агрегированных точек + буфер).
	limit := candleFetchLimit(e.cfg)
	oneMin, err := e.candleReader.FetchRecent1m(ctx, e.symbol, limit)
	if err != nil {
		log.Printf("⚠️ engine %s: чтение candles: %v", e.symbol, err)
		return
	}
	if len(oneMin) == 0 {
		// Свечей ещё нет (bot только запустился) — нормальное
		// переходное состояние, не логируем как ошибку на каждый тик.
		return
	}

	for _, tf := range allTFs {
		var candles []reader.Candle
		if tf == e.cfg.Timeframes.Base {
			candles = oneMin
		} else {
			minutes := aggregateMinutes(tf, e.cfg)
			candles = reader.Aggregate(oneMin, minutes)
		}
		if len(candles) == 0 {
			continue
		}

		prices := reader.ClosePrices(candles)
		trendCfg := trendConfigFor(tf, e.cfg)
		snap := indicator.CalcTrend(trendCfg, prices, nowMs)
		if err := e.pub.PublishTrend(ctx, tf, e.symbol, snap); err != nil {
			log.Printf("⚠️ engine %s: publish trend %s: %v", e.symbol, tf, err)
		}

		e.state.mu.Lock()
		w, ok := e.state.tradeWindows[tf]
		var buyVol, sellVol float64
		var recentTotals []float64
		if ok {
			buyVol, sellVol = w.buyVol, w.sellVol
			recentTotals = append([]float64(nil), w.recentTotals...)
		}
		e.state.mu.Unlock()

		if ok {
			volSnap := indicator.CalcVolume(volumeConfigFrom(e.cfg.Indicators.Volume), buyVol, sellVol, recentTotals, nowMs)
			if err := e.pub.PublishVolume(ctx, tf, e.symbol, volSnap); err != nil {
				log.Printf("⚠️ engine %s: publish volume %s: %v", e.symbol, tf, err)
			}
		}
	}
}

func (e *Engine) calcPressure(ctx context.Context, nowMs int64) {
	e.state.mu.Lock()
	bids := append([]indicator.OBLevel(nil), e.state.lastBids...)
	asks := append([]indicator.OBLevel(nil), e.state.lastAsks...)
	e.state.mu.Unlock()

	if len(bids) == 0 && len(asks) == 0 {
		// Стакан ещё не читался ни разу — нормально на старте, ждём
		// следующего цикла pollOrderBook.
		return
	}

	snap := indicator.CalcPressure(bids, asks, nowMs)
	if err := e.pub.PublishPressure(ctx, e.symbol, snap); err != nil {
		log.Printf("⚠️ engine %s: publish pressure: %v", e.symbol, err)
	}
}

// ---------------------------------------------------------------------------
// Вспомогательные функции — "клей" между config (данные из YAML) и
// indicator/reader (внутренние типы этих пакетов). Намеренно живут в
// engine, а не в config или indicator: ни один из тех пакетов не должен
// знать о существовании другого (config ничего не знает про indicator,
// indicator ничего не знает про config) — именно engine соединяет их,
// это его ответственность как связующего слоя.
// ---------------------------------------------------------------------------

// tfNames возвращает срез имён таймфреймов как есть — тонкая обёртка,
// нужна только чтобы вызовы вида append([]string{base}, tfNames(agg)...)
// читались как "все имена агрегированных ТФ", а не как работа напрямую
// со срезом string из config без всякого смысла-обёртки.
func tfNames(aggregates []string) []string {
	return aggregates
}

// tfDuration переводит имя таймфрейма ("1m", "8m", "24m") в time.Duration
// длины окна для tradeWindow. Разбор через time.ParseDuration: Go понимает
// "1m"/"8m" как валидные Duration-строки (минуты) без дополнительного
// парсинга вручную — "24m" тоже валиден (24 минуты), несмотря на то что
// это не "стандартный" биржевой таймфрейм вроде 1h.
//
// cfg передаётся, но не используется — оставлен на случай, если в
// будущем понадобится нестандартное сопоставление имени ТФ длительности
// (например, если introduced "24m" на самом деле должен означать что-то
// отличное от буквальных 24 минут); на сегодняшний день такого случая
// нет, поэтому имя ТФ разбирается буквально.
func tfDuration(tf string, cfg *config.Config) time.Duration {
	dur, err := time.ParseDuration(tf)
	if err != nil {
		// Некорректное имя ТФ в config.yaml должно было быть отловлено
		// в config.validate() при старте — если мы всё же здесь оказались
		// с невалидным значением, логируем и используем 1 минуту как
		// безопасный дефолт, а не паникуем в рантайме на живом сервисе.
		log.Printf("⚠️ engine: некорректный таймфрейм %q, используется 1m по умолчанию: %v", tf, err)
		return time.Minute
	}
	return dur
}

// aggregateMinutes возвращает, сколько исходных 1m-свечей формируют одну
// свечу таймфрейма tf — просто длительность tf в целых минутах. Тонкая
// обёртка вокруг tfDuration ради читаемости вызывающего кода в
// calcTrendAndVolume (aggregateMinutes(tf, cfg) читается яснее, чем
// int(tfDuration(tf, cfg).Minutes()) на каждом месте использования).
func aggregateMinutes(tf string, cfg *config.Config) int {
	return int(tfDuration(tf, cfg).Minutes())
}

// candleFetchLimit вычисляет, сколько последних 1m-свечей нужно прочитать
// из Redis, чтобы после агрегации в САМЫЙ КРУПНЫЙ настроенный таймфрейм
// хватило точек для его индикаторов (EMA slow — самый долгий период среди
// EMA/RSI/Angle на этом ТФ).
//
// Формула: largestTF_minutes * (largestTF_EMASlow + запас).
// Запас (bufferPoints) нужен, потому что:
//   - EMA технически определена и на меньшей истории, но её значение тем
//     точнее (меньше влияния произвольной точки инициализации), чем больше
//     точек ей предшествует;
//   - RSI/TrendAngle на этом же ТФ могут требовать почти столько же точек,
//     сколько EMASlow, и лучше иметь запас, чем недобирать данные молча.
//
// Пример: если "24m" настроен с ema_slow=72, largestTF=24m (24 минуты),
// то limit = 24 * (72 + 20) = 2208 минутных свечей (~36.8 часов истории).
// Осознанно НЕ ограничиваем сверху размером market:candles:1m в bot
// (config storage.candles_1m=200 по умолчанию) — если запрошено больше,
// чем реально хранится, LRANGE в reader.FetchRecent1m просто вернёт
// меньше свечей, чем limit, и агрегация/индикаторы отработают на том,
// что есть (см. len(oneMin)==0 проверку в calcTrendAndVolume).
const bufferPoints = 20

func candleFetchLimit(cfg *config.Config) int64 {
	allTFs := append([]string{cfg.Timeframes.Base}, tfNames(cfg.Timeframes.Aggregates)...)

	var largestMinutes, largestEMASlow int
	for _, tf := range allTFs {
		minutes := aggregateMinutes(tf, cfg)
		if minutes > largestMinutes {
			largestMinutes = minutes
			largestEMASlow = cfg.Indicators.Trend[tf].EMASlow
		}
	}
	if largestMinutes == 0 {
		largestMinutes = 1
	}
	return int64(largestMinutes * (largestEMASlow + bufferPoints))
}

// trendConfigFor конвертирует config.TrendParams (данные из YAML для
// одного ТФ) в indicator.TrendConfig (то, что реально требует CalcTrend).
// Единственное содержательное решение здесь — вывод UseRSI/UseMACD из
// того, заданы ли соответствующие периоды (>0) в config.yaml: явные
// булевы флаги в indicator.TrendConfig существуют для НЕГО, а config.yaml
// не заставляет пользователя дублировать "use_rsi: true" рядом с
// "rsi_period: 14" — период сам по себе однозначно говорит о намерении
// (period=0 в config.yaml означает "не считать", см. комментарии в
// config.go у TrendParams).
func trendConfigFor(tf string, cfg *config.Config) indicator.TrendConfig {
	p := cfg.Indicators.Trend[tf]
	return indicator.TrendConfig{
		EMAFast:      p.EMAFast,
		EMASlow:      p.EMASlow,
		UseRSI:       p.RSIPeriod > 0,
		RSIPeriod:    p.RSIPeriod,
		UseMACD:      p.MACDFast > 0 && p.MACDSlow > 0 && p.MACDSignal > 0,
		MACDFast:     p.MACDFast,
		MACDSlow:     p.MACDSlow,
		MACDSignal:   p.MACDSignal,
		AnglePeriods: p.AnglePeriods,
	}
}

// volumeConfigFrom конвертирует config.VolumeParams в indicator.VolumeConfig.
// Оба типа структурно идентичны (одинаковые поля), но остаются РАЗНЫМИ
// именованными типами намеренно: config/ описывает форму YAML-файла и не
// должен знать о существовании indicator/, а indicator/ — чистый пакет
// математики и не должен знать о существовании YAML вообще. Дублирование
// этих двух полей — цена за то, что оба пакета остаются независимыми
// и по отдельности тестируемыми.
func volumeConfigFrom(p config.VolumeParams) indicator.VolumeConfig {
	return indicator.VolumeConfig{
		SpikeMultiplier: p.SpikeMultiplier,
		SMAPeriod:       p.SMAPeriod,
	}
}
