// Пакет rules — чистая математика сборки T/V/P снапшотов в торговый
// сигнал (LONG/SHORT/HOLD). Не знает о Redis, о JSON, о конфигурации
// файлов — только принимает уже распарсенные снапшоты (см.
// internal/reader) и config.Config.RulesRaw, отдаёт Signal.
//
// TODO(TVP_SNIPER.md): методология сборки T+V+P в единый сигнал —
// иерархия таймфреймов "24m = Главнокомандующий, 8m = Генерал, 1m =
// быстрый ориентир" (см. analyzer/internal/indicator/trend.go), веса
// компонентов, пороги входа/выхода (например упомянутое в trend.go
// "26%-74% — запрет торговли"), тактики агрессивная/нормальная/
// консервативная (упомянуты в ESTAFETA_SIGNAL.md) — физически
// отсутствует в доступных архивах на момент написания этого файла.
// Не изобретай эти правила самостоятельно, даже правдоподобные: это
// реальные деньги на реальной бирже (см. PROMPT_NEXT.md, раздел
// "ГЛАВНОЕ ПРЕДУПРЕЖДЕНИЕ"). Пока методология не восстановлена с
// автором явными вопросами, Evaluate() обязана возвращать SignalHold
// с причиной ReasonRulesNotConfigured для любого входа — НЕ пытаться
// собрать правдоподобную формулу из одних только комментариев в коде
// analyzer.
package rules

// SignalType — направление решения signal-engine.
type SignalType string

const (
	SignalLong  SignalType = "LONG"
	SignalShort SignalType = "SHORT"
	SignalHold  SignalType = "HOLD"
)

// Reason — машиночитаемая причина решения, отдельно от SignalType.
// Нужна прежде всего для HOLD: "методология не настроена" и "T/V/P
// сейчас реально не сходятся в сигнал" — принципиально разные ситуации
// для того, кто будет читать сигналы (человек в TUI или лог), и их не
// стоит смешивать в одну строку без структуры.
type Reason string

const (
	// ReasonRulesNotConfigured — единственная причина, которую эта
	// заглушка умеет возвращать: правила сборки сигнала ещё не
	// реализованы, потому что методология TVP_SNIPER не восстановлена.
	// См. TODO в шапке файла.
	ReasonRulesNotConfigured Reason = "rules_not_configured"
)

// Signal — результат Evaluate().
type Signal struct {
	Type   SignalType
	Reason Reason
}

// Input — все снапшоты T/V/P для одного символа на момент оценки.
// Поля намеренно определены как map по таймфрейму для Trend/Volume
// (не как отдельные поля TF1m/TF8m/TF24m) — это позволяет будущей
// реализации Evaluate() перебирать таймфреймы по конфигу
// (config.Config.Timeframes), не меняя сигнатуру Input при появлении
// новых таймфреймов.
type Input struct {
	// Trend — снапшоты T по таймфрейму ("1m"/"8m"/"24m").
	Trend map[string]TrendInput
	// Volume — снапшоты V по таймфрейму.
	Volume map[string]VolumeInput
	// Pressure — снапшот P, один на символ (не по таймфрейму).
	Pressure PressureInput
}

// TrendInput/VolumeInput/PressureInput намеренно НЕ переиспользуют
// типы напрямую из internal/reader — reader описывает JSON-протокол
// Redis (внешний формат, могут появиться доп. поля вроде метаданных
// TTL), а rules должен видеть только то, что реально нужно для
// принятия решения. Дублирование полей здесь — осознанный выбор ради
// независимости пакетов: reader может измениться (например добавить
// поле), не заставляя rules пересматривать сигнатуру Evaluate.
type TrendInput struct {
	EMAFast       float64
	EMASlow       float64
	Direction     string // "up" / "down" / "neutral"
	Angle         float64
	RSI           float64
	MACDHistogram float64
}

type VolumeInput struct {
	BuyVol  float64
	SellVol float64
	Delta   float64
	Spike   bool
}

type PressureInput struct {
	BidVol    float64
	AskVol    float64
	Imbalance float64
}

// Evaluate принимает снапшоты T/V/P для одного символа и возвращает
// торговый сигнал.
//
// ЗАГЛУШКА: пока методология TVP_SNIPER не восстановлена с автором,
// эта функция ВСЕГДА возвращает SignalHold/ReasonRulesNotConfigured,
// независимо от содержимого input. Это осознанное, а не забытое
// поведение — см. TODO в шапке файла.
func Evaluate(input Input) Signal {
	_ = input // пока не используется — см. TODO выше
	return Signal{
		Type:   SignalHold,
		Reason: ReasonRulesNotConfigured,
	}
}
