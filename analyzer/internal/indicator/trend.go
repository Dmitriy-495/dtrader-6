package indicator

// TrendConfig — настройки расчёта T на ОДНОМ таймфрейме. Разные
// таймфреймы используют разные периоды и разный набор вспомогательных
// индикаторов — см. TVP_SNIPER.md:
//   - 24m ("Главнокомандующий"): EMA fast/slow + Trend Angle + RSI
//   - 8m  ("Генерал"):            EMA fast/slow + MACD
//   - 1m:                         EMA fast/slow (быстрый ориентир)
//
// UseRSI/UseMACD — явные флаги, а не "просто передать period=0 чтобы
// отключить": так конфиг однозначно говорит, какие поля TrendSnapshot
// вообще имеют смысл на этом ТФ, и engine/ не задаётся вопросом,
// является ли RSI=0 "выключено" или "посчитано и ровно 50".
type TrendConfig struct {
	EMAFast int
	EMASlow int

	UseRSI    bool
	RSIPeriod int

	UseMACD    bool
	MACDFast   int
	MACDSlow   int
	MACDSignal int

	// AnglePeriods — сколько последних цен брать для TrendAngle.
	// 0 означает "не считать угол на этом ТФ".
	AnglePeriods int
}

// Direction — направление тренда, выведенное из взаимного положения
// EMAFast/EMASlow. Строковый тип (а не bool) — потому что кроме "вверх"
// и "вниз" осмысленно существует "нейтрально" (EMA почти равны), и
// signal-engine должен уметь отличить "чёткий тренд" от "рынок в
// нерешительности" — bool такое различие потерял бы.
type Direction string

const (
	DirectionUp      Direction = "up"
	DirectionDown    Direction = "down"
	DirectionNeutral Direction = "neutral"
)

// TrendSnapshot — результат расчёта T на одном таймфрейме для одного
// символа. Именно эта структура (в виде JSON) публикуется в Redis по
// ключу indicators:trend:{tf}:{symbol} — см. publisher/redis.go.
type TrendSnapshot struct {
	EMAFast   float64   `json:"ema_fast"`
	EMASlow   float64   `json:"ema_slow"`
	Direction Direction `json:"direction"`

	// Angle/RSI/MACDHistogram — 0, если соответствующий расчёт не
	// включён в TrendConfig для этого ТФ (см. UseRSI/UseMACD/AnglePeriods
	// выше). Сознательно не используем указатели (*float64) ради
	// простоты JSON и работы с ним в signal-engine — если понадобится
	// различать "0.0 посчитано" от "не считалось", решать по TrendConfig
	// уже на стороне потребителя, а не через nil-проверки здесь.
	Angle         float64 `json:"angle"`
	RSI           float64 `json:"rsi"`
	MACDHistogram float64 `json:"macd_histogram"`

	Ts int64 `json:"ts"`
}

// neutralEpsilon — минимальная относительная разница между EMAFast и
// EMASlow, ниже которой направление считается "neutral", а не
// "up"/"down". Без этого порога тренд бы дёргался между up/down на
// незначимых колебаниях, когда цена находится во флэте и EMA почти
// совпадают — а именно такие колебания сильнее всего портят сигнал в
// сайдвее, который TVP_SNIPER.md явно выделяет как "нейтральную зону"
// (раздел про пороги HTF: 26%-74% - запрет торговли).
const neutralEpsilon = 0.0005 // 0.05% относительной разницы

// CalcTrend считает T на одном таймфрейме по конфигу cfg и срезу цен
// закрытия prices (хронологический порядок, старые → новые). ts —
// unix-время в мс, когда считается снапшот (передаётся снаружи, а не
// берётся через time.Now() внутри — так CalcTrend остаётся чистой
// функцией без побочных эффектов, что упрощает юнит-тесты).
func CalcTrend(cfg TrendConfig, prices []float64, ts int64) TrendSnapshot {
	snap := TrendSnapshot{Ts: ts}
	if len(prices) == 0 {
		return snap
	}

	snap.EMAFast = EMA(prices, cfg.EMAFast)
	snap.EMASlow = EMA(prices, cfg.EMASlow)
	snap.Direction = directionFrom(snap.EMAFast, snap.EMASlow)

	if cfg.AnglePeriods > 0 {
		periods := cfg.AnglePeriods
		if periods > len(prices) {
			periods = len(prices)
		}
		snap.Angle = TrendAngle(prices[len(prices)-periods:])
	}

	if cfg.UseRSI {
		snap.RSI = RSI(prices, cfg.RSIPeriod)
	}

	if cfg.UseMACD {
		macd := MACD(prices, cfg.MACDFast, cfg.MACDSlow, cfg.MACDSignal)
		snap.MACDHistogram = macd.Histogram
	}

	return snap
}

// directionFrom сравнивает EMAFast и EMASlow с учётом neutralEpsilon,
// чтобы избежать дребезга направления на почти равных значениях (см.
// комментарий у neutralEpsilon выше).
func directionFrom(emaFast, emaSlow float64) Direction {
	if emaSlow == 0 {
		return DirectionNeutral
	}
	relDiff := (emaFast - emaSlow) / emaSlow
	switch {
	case relDiff > neutralEpsilon:
		return DirectionUp
	case relDiff < -neutralEpsilon:
		return DirectionDown
	default:
		return DirectionNeutral
	}
}
