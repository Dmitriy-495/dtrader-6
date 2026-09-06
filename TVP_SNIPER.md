# TVP Sniper Reversal Engine

_Версия 3.0 | Futures Only_
_Базовый актив: BTCUSDT-PERP | ETHUSDT-PERP_
_Таймфреймы: 24M (HTF) / 6M (MTF) / Tick & Order Book (LTF)_
_Стек реализации: Go (адаптировано из первоначального замысла на Node.js + TypeScript)_

## О ЭТОМ ДОКУМЕНТЕ

Этот файл — восстановленная версия `TVP_SNIPER.md`, на который ссылается
код `analyzer` в `dtrader-6`, но который считался безвозвратно
утерянным. Автор нашёл исходную v1.0 (сгенерирована DeepSeek AI) и
полный диалог её эволюции до v2.0 и v3.0 "Reversal Engine" в архиве
предыдущей версии проекта.

**Этот документ — v3.0, дописанная в сессии 2026-09-03.** Диалог,
из которого он восстановлен, обрывался на середине секции тактик
реверса. Часть параметров дописана автором явно в этой сессии, часть
**намеренно оставлена как открытые вопросы** (см. пометки `TODO`
по тексту) — методология не додумывается за автора там, где он не
называл число.

**⚠️ КРИТИЧНО: несовпадение таймфреймов с текущим кодом.**
Всюду в этом документе (v1.0, v2.0, v3.0) архитектура строится на
**двух** уровнях — HTF (24m) и MTF (6m). Реальный `analyzer`,
работающий на проде, считает **три** таймфрейма — 1m/8m/24m. Это
расхождение **не разрешено в рамках этого документа** — по прямому
решению автора это отдельный, более важный разговор, который нужно
провести до переноса методологии в `signal-engine/internal/rules`.
До тех пор этот документ описывает мышление и формулы стратегии в
исходных величинах (24m/6m), а не финальную боевую конфигурацию.

**Смена стека:** первоначальный диалог задумывал реализацию на
Node.js + TypeScript. Весь код в этом документе переписан на Go —
это реальный стек `dtrader-6` (`bot`, `analyzer`, `signal-engine`
уже на Go). Переносятся идеи, формулы и структуры данных; ни один
фрагмент TypeScript-кода не был скопирован дословно.

---

## 🎯 Философия стратегии

**"Не закрывай — трансформируй позицию"**

TVP Sniper Reversal Engine — эволюция импульсной мультитаймфреймовой
стратегии (TVP = Time, Volume, Pressure), где вместо простого
закрытия позиции при сигнале разворота мы переходим в хеджированное
состояние: открываем противоположную позицию, не закрывая исходную,
и затем плавно разрешаем этот хедж в зависимости от того, подтвердился
разворот или оказался ложным.

### Ключевые принципы

- **Беспрерывная экспозиция** — стратегия всегда в рынке, нет паузы
  между закрытием одной позиции и открытием следующей
- **Плавный реверс** — переход между направлениями происходит через
  промежуточное хеджированное состояние, а не мгновенным разворотом
- **Защита от ложных разворотов** — если сигнал реверса окажется
  ложным, исходная позиция остаётся нетронутой, хедж просто снимается
- **Без тейк-профитов** — закрытие позиций только на основании
  сигналов, симметричных сигналам открытия, а не фиксированных
  уровней прибыли; это осознанный выбор автора (см. раздел
  "Управление позициями" ниже)
- **Maker-ориентированность** — минимизация комиссий через создание
  ликвидности лимитными ордерами, а не рыночными

---

## 📊 Архитектура системы принятия решений

### 1. Главнокомандующий (HTF — 24 минуты) — 3 индикатора

**Назначение:** определение глобального тренда, ключевых объёмных зон
и вероятности разворота.

| Индикатор       | Вес | Параметры          | Критерии                  |
| :-------------- | :-- | :------------------ | :------------------------- |
| Trend Angle     | 40% | 20 периодов          | ≥ 15° для тренда           |
| EMA Cluster     | 35% | EMA(72) + EMA(144)   | Пересечение и наклон       |
| Volume Profile  | 25% | POC + Value Area     | Кластерный анализ объёмов  |

```go
// HTFSignal — результат анализа уровня Главнокомандующего.
type TrendDirection string

const (
	TrendStrongBull TrendDirection = "STRONG_BULL"
	TrendBull       TrendDirection = "BULL"
	TrendNeutral    TrendDirection = "NEUTRAL"
	TrendBear       TrendDirection = "BEAR"
	TrendStrongBear TrendDirection = "STRONG_BEAR"
)

type KeyLevels struct {
	POC          float64 // Point of Control — цена с максимальным объёмом
	ValueAreaHigh float64
	ValueAreaLow  float64
}

type HTFSignal struct {
	TrendScore          float64 // 0-100
	TrendDirection      TrendDirection
	KeyLevels           KeyLevels
	ReversalProbability float64 // 0-1
}
```

**Пороги принятия решений (унаследованы из v1.0/v2.0, не менялись в v3.0):**

- Разрешение на LONG: суммарный счёт ≥ 75%
- Разрешение на SHORT: суммарный счёт ≤ 25%
- Нейтральная зона: **26%-74% — запрет торговли**

Это и есть источник комментария `"26%-74% — запрет торговли"` в
`analyzer/internal/indicator/trend.go` — формула родилась здесь, на
уровне Главнокомандующего v1.0, и не менялась ни в одной последующей
версии стратегии.

### 2. Генерал (MTF — 6 минут) — 4 индикатора

**Назначение:** обнаружение импульса разворота и точек входа в хедж.

| Индикатор            | Вес | Параметры    | Критерии                        |
| :-------------------- | :-- | :------------ | :-------------------------------- |
| MTF Trend Angle       | 30% | 12 периодов    | Резкое изменение ≥ 10°            |
| ROC + Volume Power    | 30% | 3 периода      | Combined Score < 0.3 для реверса  |
| EMA Momentum          | 25% | EMA(24)        | Пересечение с EMA(50)             |
| Momentum Divergence   | 15% | RSI + Price    | Медвежья/бычья дивергенция        |

**Эволюция этого раздела важна для понимания кода `analyzer`:** в
v1.0 здесь стоял `MACD Signal` (вес 15%) — именно поэтому в
`analyzer/config.yaml` MACD включён только на 8m (ближайший
исторический аналог 6m-Генерала). В v2.0 MACD был сознательно убран
как избыточный (дублирует EMA Momentum, запаздывающий осциллятор) и
заменён на `MTF Trend Angle` + комбинированный `ROC + Volume Power`.
В v3.0 состав немного изменился ещё раз — вместо `Support/Resistance`
добавлен `Momentum Divergence`, поскольку для обнаружения разворота
(а не только входа) дивергенция цены и осциллятора более
информативна, чем пробитие уровня.

```go
// ROCVolumePower — комбинированный индикатор скорости и силы,
// введён в v2.0 как замена MACD.
type ROCVolumePowerResult struct {
	ROC           float64 // Rate of Change, %
	VolumeRatio   float64 // текущий объём / средний объём
	CombinedScore float64
}

func CalculateROCVolumePower(currentPrice, priceNPeriodsAgo, currentVolume, avgVolume float64) ROCVolumePowerResult {
	priceROC := ((currentPrice - priceNPeriodsAgo) / priceNPeriodsAgo) * 100
	volumePower := currentVolume / avgVolume

	// Веса 0.6/0.4 зафиксированы в исходном диалоге (v2.0), не менялись в v3.0.
	combinedScore := (priceROC * 0.6) + (volumePower * 0.4)

	return ROCVolumePowerResult{
		ROC:           priceROC,
		VolumeRatio:   volumePower,
		CombinedScore: combinedScore,
	}
}
```

### 3. Снайпер (LTF — Tick & Order Book) — 3 компонента

**Назначение:** точное исполнение реверс-ордеров и управление хеджем.

| Компонент               | Вес | Параметры    | Критерии                       |
| :------------------------ | :-- | :------------ | :-------------------------------- |
| Order Book Imbalance      | 40% | 10 уровней     | Ratio > 2.0 или < 0.5             |
| VWAP Momentum             | 35% | Real-time VWAP | Смена направления                 |
| Liquidity Absorption      | 25% | Крупные ордера | Поглощение на ключевых уровнях    |

VWAP на уровне Снайпера появился в v2.0 (заменил `Spread & Tick
Momentum`) — обоснование: VWAP объединяет цену и объём в одном
индикаторе, что напрямую соответствует философии Time/Volume/Pressure,
а на очень малых таймфреймах спред сам по себе даёт меньше сигнала,
чем отклонение цены от объёмно-взвешенного среднего.

```go
// VWAPSignal — анализ момента VWAP, взят из v2.0 без изменений в v3.0.
type VWAPSignal struct {
	Bullish bool
	Score   float64 // 0-1
	Details VWAPSignalDetails
}

type VWAPSignalDetails struct {
	PriceVsVWAP        bool // цена выше VWAP
	VWAPDirection      bool // VWAP растёт
	VolumeConfirmation bool // объём выше среднего
}

func AnalyzeVWAPSignal(currentPrice, vwapValue, vwapSlope, vwapVolumeRatio float64) VWAPSignal {
	priceVsVWAP := currentPrice > vwapValue
	vwapDirection := vwapSlope > 0
	volumeConfirmation := vwapVolumeRatio > 1.2

	score := 0.0
	if priceVsVWAP {
		score += 0.4
	}
	if vwapDirection {
		score += 0.4
	}
	if volumeConfirmation {
		score += 0.2
	}

	return VWAPSignal{
		Bullish: score >= 0.6,
		Score:   score,
		Details: VWAPSignalDetails{
			PriceVsVWAP:        priceVsVWAP,
			VWAPDirection:      vwapDirection,
			VolumeConfirmation: volumeConfirmation,
		},
	}
}
```

---

## 🔄 Reversal Engine: механизм реверса

### Состояния позиции

```go
type PositionState string

const (
	PositionLongFull      PositionState = "LONG_FULL"      // 100% лонг
	PositionLongHedged    PositionState = "LONG_HEDGED"    // лонг + частичный шорт-хедж
	PositionNeutralHedged PositionState = "NEUTRAL_HEDGED" // 50/50 хедж
	PositionShortHedged   PositionState = "SHORT_HEDGED"   // шорт + частичный лонг-хедж
	PositionShortFull     PositionState = "SHORT_FULL"     // 100% шорт
)
```

### Процесс реверса LONG → SHORT (детально проработан в исходном диалоге)

**Фаза 1: обнаружение сигнала реверса.** Критерии активации строже,
чем простой сигнал выхода (в v2.0 выход из LONG требовал HTF < 40% /
MTF < 35% / LTF < 0.9 — здесь пороги ниже, потому что реверс — более
серьёзное решение, чем просто закрытие):

```go
type ReversalType string

const (
	ReversalLongToShort ReversalType = "LONG_TO_SHORT"
	ReversalShortToLong ReversalType = "SHORT_TO_LONG"
)

type ReversalSignal struct {
	Type         ReversalType
	Confidence   float64
	TriggerPrice float64
	TimestampMs  int64
}

// DetectReversalSignal проверяет условия активации реверса для LONG-позиции.
// Пороги (30/25/0.7) зафиксированы в исходном диалоге для LONG→SHORT.
func DetectReversalSignal(currentPosition PositionState, htf HTFSignal, mtfImpulse, ltfPressure float64, clusterBroken bool) *ReversalSignal {
	if currentPosition == PositionLongFull &&
		htf.TrendScore < 30 &&
		mtfImpulse < 25 &&
		ltfPressure < 0.7 &&
		clusterBroken {
		return &ReversalSignal{
			Type:       ReversalLongToShort,
			Confidence: calculateReversalConfidence(htf, mtfImpulse, ltfPressure),
		}
	}
	return nil
}
```

**TODO (SHORT → LONG):** исходный диалог прорабатывает только сценарий
LONG→SHORT в деталях. Для симметричного случая (открыта SHORT-позиция,
обнаруживается сигнал разворота вверх) по духу стратегии условия
должны быть зеркальными (`htf.TrendScore > 70`, `mtfImpulse > 75`,
`ltfPressure > 1.3` — обратные величины от порогов LONG→SHORT), но
**это не было явно проговорено с автором** и не должно кодироваться
как факт без подтверждения. Обсудить отдельно перед реализацией в
`signal-engine`.

**Фаза 2: инициализация хеджирования.**

```go
type HedgeSizeParams struct {
	PositionSize float64
	Confidence   float64
	Volatility   float64 // например, ATR как доля от цены
}

// CalculateHedgeSize — формула из исходного диалога (v3.0), без изменений.
// baseHedge = 50% — это общий базовый размер хеджа "по умолчанию",
// НЕ привязанный к конкретной тактике (агрессивной/консервативной/
// скальперской) — см. раздел "Тактики реверса" ниже, где базовые
// проценты для каждой тактики отличаются от этой общей формулы.
func CalculateHedgeSize(p HedgeSizeParams) float64 {
	baseHedge := p.PositionSize * 0.5

	confidenceBoost := 0.0
	if p.Confidence > 0.8 {
		confidenceBoost = 0.2
	}

	volatilityAdjustment := 0.0
	if p.Volatility > 0.04 {
		volatilityAdjustment = -0.1
	}

	hedge := baseHedge + confidenceBoost + volatilityAdjustment
	if hedge > 0.8 {
		hedge = 0.8
	}
	if hedge < 0.3 {
		hedge = 0.3
	}
	return hedge
}
```

**Фаза 3: активное управление хеджем.** Матрица решений — сценарии
подтверждения реверса, ложного сигнала, полного разворота и
принудительного закрытия:

```go
type HedgeDecision string

const (
	DecisionConfirmReversal  HedgeDecision = "CONFIRM_REVERSAL"
	DecisionReduceHedge      HedgeDecision = "REDUCE_HEDGE"
	DecisionCompleteReversal HedgeDecision = "COMPLETE_REVERSAL"
	DecisionForceCloseHedge  HedgeDecision = "FORCE_CLOSE_HEDGE"
	DecisionMaintainHedge    HedgeDecision = "MAINTAIN_HEDGE"
)

type HedgedPositionState struct {
	NetExposure   float64 // + = чистый лонг, - = чистый шорт
	HoursInHedge  float64
}

type ExitSignals struct {
	HTFTrendScore float64
	MTFImpulse    float64
}

// ManageHedgedPosition — матрица принятия решений из исходного диалога,
// без изменений. Максимум 48 часов на хедж — фиксированный лимит,
// защита от "зависшего" хеджа.
func ManageHedgedPosition(state HedgedPositionState, signals ExitSignals) HedgeDecision {
	switch {
	case signals.HTFTrendScore < 20 && signals.MTFImpulse < 15 && state.NetExposure > -0.8:
		return DecisionConfirmReversal
	case signals.HTFTrendScore > 60 && signals.MTFImpulse > 50 && state.HoursInHedge < 12:
		return DecisionReduceHedge
	case state.NetExposure < -0.6 && state.HoursInHedge > 2:
		// TODO: reversalConfirmed(signals) из исходного диалога был
		// отдельной, неопределённой функцией — здесь её роль частично
		// покрыта условием NetExposure < -0.6, но точный критерий
		// "подтверждения" стоит явно сверить с автором перед реализацией.
		return DecisionCompleteReversal
	case state.HoursInHedge >= 48:
		return DecisionForceCloseHedge
	default:
		return DecisionMaintainHedge
	}
}
```

---

## 🎮 Тактики реверса

Три тактики отличаются требуемой уверенностью сигнала (`confidence`)
и, соответственно, агрессивностью хеджирования. Общая структура одной
тактики: **условия активации → начальный % хеджа → тайминг проверки
усиления → порог Net Exposure для полного реверса**.

### Агрессивный реверс (High Confidence)

**Условия:**
- HTF Trend Score < 20
- MTF Impulse < 15
- LTF Pressure < 0.6
- Объёмный кластер пробит

**Действия:**
- Начальный хедж: **70%**
- Проверка подтверждения (усиление хеджа): через **30 минут**
- Полный реверс при: Net Exposure < **-0.8**

### Консервативный реверс (Medium Confidence)

**Условия:**
- HTF Trend Score < 35
- MTF Impulse < 30
- LTF Pressure < 0.8
- Подтверждение дивергенции

**Действия:**
- Начальный хедж: **40%**
- Проверка подтверждения: через **2 часа**
- Полный реверс при: Net Exposure < **-0.6**

### Скальперский реверс (Range Market)

Задуман для рынка в диапазоне (флэт), а не для сильного трендового
движения — в отличие от агрессивной и консервативной тактик, здесь
цель не полный тренд-разворот, а истощение локального движения внутри
диапазона.

**Условия:**
- Рынок в диапазоне
- Подход к границам кластера
- LTF показывает exhaustion (истощение импульса)
- ⚠️ **TODO: четвёртое условие не определено.** У агрессивной тактики
  четвёртым условием служит "объёмный кластер пробит", у
  консервативной — "подтверждение дивергенции". Симметричный
  кандидат для флэта обсуждался (например, LTF Order Book Pressure
  в нейтральном диапазоне 0.8-1.2, либо отсутствие пробоя VAH/VAL —
  цена внутри Value Area), но ни один вариант не был подтверждён
  автором. **Не кодировать до явного решения.**

**Действия:**
- Начальный хедж: **25-30%** (подтверждено автором в этой сессии —
  меньше, чем у консервативной тактики (40%), поскольку уверенность в
  реальном развороте здесь ниже: цель — истощение локального
  движения, а не полноценный тренд-разворот)
- ⚠️ **TODO: тайминг проверки подтверждения не определён.** У
  агрессивной — 30 минут, у консервативной — 2 часа. Логично, что для
  флэта интервал должен быть короче консервативного (движения внутри
  диапазона быстрее себя исчерпывают), но конкретное число не
  зафиксировано. **Обсудить отдельно перед реализацией.**
- ⚠️ **TODO: порог Net Exposure для полного реверса не определён.**
  У агрессивной -0.8, у консервативной -0.6. **Обсудить отдельно.**

```go
// ReversalTactic описывает один из трёх режимов реверса.
// Значения ScalperTactic содержат нулевые поля там, где параметр
// ещё не согласован с автором — см. TODO выше. Использовать
// ScalperTactic для реальных решений ДО заполнения этих полей
// запрещено (см. общее правило "не изобретай торговые правила сам"
// в PROMPT_NEXT.md).
type ReversalTactic struct {
	Name                   string
	InitialHedgePercent    float64
	ConfirmationCheckAfter time.Duration
	FullReversalThreshold  float64 // порог Net Exposure
}

var AggressiveTactic = ReversalTactic{
	Name:                   "aggressive",
	InitialHedgePercent:    0.70,
	ConfirmationCheckAfter: 30 * time.Minute,
	FullReversalThreshold:  -0.8,
}

var ConservativeTactic = ReversalTactic{
	Name:                   "conservative",
	InitialHedgePercent:    0.40,
	ConfirmationCheckAfter: 2 * time.Hour,
	FullReversalThreshold:  -0.6,
}

// ScalperTactic — НЕПОЛНАЯ. ConfirmationCheckAfter и
// FullReversalThreshold нулевые до явного решения автора.
var ScalperTactic = ReversalTactic{
	Name:                "scalper",
	InitialHedgePercent: 0.275, // середина диапазона 25-30%, подтверждённого автором
	// ConfirmationCheckAfter: TODO — не определено
	// FullReversalThreshold:  TODO — не определено
}
```

---

## ⚙️ Фьючерс-специфичные настройки

Эти разделы взяты из исходного диалога практически без изменений
(кроме перевода на Go) — они не зависели от обрыва диалога и были
проработаны до конца.

### Учёт финансирования (Funding Rate)

```go
type FundingAction string

const (
	FundingProceed         FundingAction = "PROCEED"
	FundingDelayHedge      FundingAction = "DELAY_HEDGE"
	FundingPreferShortHedge FundingAction = "PREFER_SHORT_HEDGE"
)

type FundingDecision struct {
	Action          FundingAction
	Reason          string
	FundingRate     float64
	NextFundingInMs int64
}

// CheckFundingImpact — логика из исходного диалога без изменений.
// Избегаем открытия хеджа непосредственно перед крупной выплатой
// funding и используем отрицательный funding в свою пользу для шорт-хеджа.
func CheckFundingImpact(fundingRate float64, nextFundingInMs int64) FundingDecision {
	const oneHourMs = 60 * 60 * 1000

	if abs(fundingRate) > 0.01 && nextFundingInMs < oneHourMs {
		return FundingDecision{
			Action:          FundingDelayHedge,
			Reason:          "HIGH_FUNDING_RATE",
			FundingRate:     fundingRate,
			NextFundingInMs: nextFundingInMs,
		}
	}

	if fundingRate < -0.005 {
		return FundingDecision{
			Action:      FundingPreferShortHedge,
			Reason:      "POSITIVE_FUNDING_FOR_SHORT",
			FundingRate: fundingRate,
		}
	}

	return FundingDecision{Action: FundingProceed}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
```

### Кредитное плечо и маржа

```go
const (
	MaxLeverage   = 10.0
	SafetyMargin  = 0.3 // 30% запас прочности от баланса счёта
)

// CalculateOptimalLeverage — логика из исходного диалога без изменений.
func CalculateOptimalLeverage(volatility float64) float64 {
	baseLeverage := 5.0
	volatilityAdjustment := 0.0
	if volatility > 0.04 {
		volatilityAdjustment = -2.0
	}

	leverage := baseLeverage + volatilityAdjustment
	if leverage > MaxLeverage {
		return MaxLeverage
	}
	return leverage
}

type Position struct {
	InitialMargin float64
}

type MarginStatus struct {
	CanOpenNewPosition bool
	MarginUsageRatio   float64
	AvailableForHedge  float64
}

func CheckMarginRequirements(positions []Position, accountBalance float64) MarginStatus {
	var totalMargin float64
	for _, p := range positions {
		totalMargin += p.InitialMargin
	}
	availableMargin := accountBalance * SafetyMargin

	return MarginStatus{
		CanOpenNewPosition: totalMargin < availableMargin,
		MarginUsageRatio:   totalMargin / accountBalance,
		AvailableForHedge:  availableMargin - totalMargin,
	}
}
```

---

## 🛡️ Risk-Management для хеджированных позиций

**⚠️ Этот раздел — самое неполное место документа.** В исходном
диалоге risk-management для одновременных LONG+SHORT позиций на одном
инструменте обсуждался только фрагментарно, тремя отдельными
константами без единой модели:

```go
// Три ограничения, упомянутые в исходном диалоге как отдельные идеи,
// НЕ как согласованная единая модель риска:
const (
	// MaxHedgeCostRatio — лимит на комиссии хеджирования как доля от
	// баланса счёта. В диалоге: "0.2% от депозита".
	MaxHedgeCostRatio = 0.002

	// MaxHedgeRatio — максимальный размер хеджа относительно основной
	// позиции. В диалоге: "не более 80% от основной позиции" —
	// совпадает с верхней границей CalculateHedgeSize выше.
	MaxHedgeRatio = 0.8

	// MaxHedgeHours — максимальное время удержания хеджа, после
	// которого он закрывается принудительно независимо от исхода
	// (используется в ManageHedgedPosition выше как DecisionForceCloseHedge).
	MaxHedgeHours = 48
)
```

**Чего здесь принципиально не хватает** (не додумано ни в диалоге, ни
в этой сессии, требует отдельного разговора с автором до реализации):

1. **Как считать общую экспозицию счёта, когда одновременно открыты
   LONG и SHORT по одному инструменту.** В v2.0 (без хеджирования)
   правило было простым: "общая экспозиция ≤ 15% от депозита, не
   более 3 одновременных сделок". С хеджем неочевидно, считать ли
   валовую экспозицию (LONG + SHORT суммарно) или чистую
   (`NetExposure`, как в `HedgedPositionState` выше) — от этого
   решения зависит вся дальнейшая формула размера позиции.
2. **Катастрофический стоп для хеджированной пары.** В v2.0 был
   единый катастрофический стоп "-15% от депозита на сделку". Неясно,
   применяется ли это к каждой ноге хеджа отдельно, к паре целиком,
   или к чистой экспозиции.
3. **Влияние двойной маржи на `LeverageManager`.** `CheckMarginRequirements`
   выше суммирует `InitialMargin` по всем позициям — то есть уже
   учитывает, что хедж потребляет дополнительную маржу сверх основной
   позиции, но нет согласованного правила, при какой загрузке маржи
   стратегия должна отказываться от открытия хеджа вообще (не только
   от новой независимой сделки).
4. **Взаимодействие лимитов из v2.0** (≤15% депозита общая экспозиция,
   ≤3 одновременных сделки, максимальная просадка на сделку 4%) **с
   новой моделью хеджирования** — эти лимиты писались до идеи
   Reversal Engine и не были пересмотрены под неё.

**Не кодировать risk-guard для хеджированных позиций в
`signal-engine`/будущем `risk-guard` до отдельного разговора с
автором по этим четырём пунктам.**

---

## 📈 Управление позициями (без тейк-профитов)

Согласовано явно и однозначно в исходном диалоге: **стратегия не
использует тейк-профиты**. Все закрытия позиций происходят на
основании тех же типов сигналов, что и открытие, но в противоположную
сторону — символично тому, как реверс заменяет простое закрытие.

- **Нет фиксированных целей по прибыли.** Позиция остаётся открытой,
  пока сигналы (HTF/MTF/LTF) продолжают подтверждать направление.
- **Защитные стопы — только на удалении, как страховка от чёрных
  лебедей**, не как основной механизм выхода. В v2.0 обсуждались
  величины вроде "2.5×ATR от цены входа" и "-15% от депозита на
  сделку" как катастрофический стоп — но см. пункт 2 в разделе
  Risk-Management выше: неясно, как эти цифры адаптируются под
  хеджированные позиции v3.0.
- **Reversal Engine заменяет обычный выход.** Вместо "закрыть
  позицию по сигналу разворота" (как было в v1.0/v2.0) в v3.0 сигнал
  разворота **открывает хедж**, а не закрывает позицию напрямую —
  закрытие исходной позиции происходит только на фазе
  `DecisionCompleteReversal` (см. `ManageHedgedPosition`).

---

## 🧪 Параметры для тестирования и оптимизации

Этот раздел в исходном диалоге отсутствовал для v3.0 — параметры ниже
перенесены из v1.0/v2.0 там, где формула не изменилась в v3.0, и
явно помечены как отсутствующие там, где v3.0 ввела новые сущности
без диапазонов тестирования.

| Параметр                     | Номинальное значение | Диапазон тестирования | Статус                          |
| :---------------------------- | :-------------------- | :---------------------- | :-------------------------------- |
| HTF Trend Angle Threshold      | 15°                    | 10°-25°                  | Не менялось с v1.0                |
| MTF Trend Angle (Генерал)      | 8°                     | 5°-15°                   | Введено в v2.0, не менялось       |
| ROC + Volume Threshold         | 0.8                    | 0.5-1.2                  | Введено в v2.0, не менялось       |
| Order Book Pressure Ratio      | 1.5                    | 1.3-1.8                  | Не менялось с v1.0                |
| Volume Spike Multiplier        | 2.5×                   | 2.0×-3.5×                | Не менялось с v1.0                |
| Hedge Confidence Boost         | 0.8 (порог)            | —                        | ⚠️ TODO: диапазон не определён    |
| Hedge Volatility Threshold     | 0.04                   | —                        | ⚠️ TODO: диапазон не определён    |
| Max Leverage                   | 10×                    | —                        | ⚠️ TODO: диапазон не определён    |
| Max Hedge Duration             | 48 часов               | —                        | ⚠️ TODO: диапазон не определён    |

**Критерии успешности тестирования** (из v2.0, не пересматривались
для v3.0 — с появлением хеджирования профиль риска/доходности
меняется, эти цифры стоит пересмотреть после бэктестинга):

- Profit Factor: > 1.8
- Maximum Drawdown: < 6%
- Sharpe Ratio: > 1.5
- Win Rate: > 58%
- Average Win/Average Loss Ratio: > 2.0

---

## 🚀 Запуск и мониторинг

Этот раздел отсутствовал в исходном диалоге для v3.0. Ниже —
адаптация процесса из v1.0/v2.0 под специфику хеджированных позиций
на фьючерсах; сама последовательность шагов подтверждена, но
конкретные числа (кроме явно перенесённых) не согласованы с автором.

### Инициализация системы

1. Калибровка индикаторов на исторических данных (аналогично v2.0:
   1500+ свечей)
2. Тестирование `HedgeManager`/`ReversalDetector` в режиме демо —
   **обязательно до реальных ордеров**, независимо от того, что уже
   работает на других сервисах (см. общее правило проекта "только
   логировать сигналы, ордера — отдельное решение" в
   `CHECKPOINT_20260902.md`)
3. Постепенное увеличение размера позиции — из v2.0: от 25% до 100%,
   применимость к хеджированным позициям не пересмотрена

### Мониторинг в реальном времени

- Логирование всех решений с весовыми коэффициентами (как в v1.0/v2.0)
- Отдельное логирование состояния каждого активного хеджа: `NetExposure`,
  `HoursInHedge`, последнее принятое `HedgeDecision`
- Алерты при приближении к `MaxHedgeHours` (48 часов) заранее, не
  только в момент принудительного закрытия
- Ежедневная верификация корректности расчёта funding rate — влияние
  этого параметра на решения `FundingRateManager` требует более
  частой проверки, чем обычные индикаторы, поскольку funding
  начисляется периодически, а не непрерывно

---

## 💰 Комиссии и структура ордеров (Gate.io Futures)

Отдельная тема, обсуждённая в диалоге после составления архитектуры
v3.0 — здесь зафиксирована для полноты, поскольку прямо влияет на
частоту перебалансировки хеджей.

### Maker vs Taker

- **Maker** ("создатель ликвидности") — лимитный ордер, который не
  исполняется сразу, а становится частью стакана. Комиссия ниже.
- **Taker** ("забирающий ликвидность") — ордер, который сразу
  находит противоположную заявку и исполняется. Комиссия выше.

На Gate.io Futures (на момент обсуждения в диалоге): maker ≈ 0.02%,
taker ≈ 0.05%. **Актуальные ставки стоит сверить перед запуском** —
это внешние данные биржи, которые могли измениться.

### Рекомендация для Reversal Engine

Поскольку механизм хеджирования подразумевает частую перебалансировку
(открытие/увеличение/уменьшение хеджа), стоимость комиссий особенно
чувствительна к доле maker-ордеров:

```go
// PlaceMakerOrder — концептуальный пример: сначала пробуем встать
// в стакан лимитным ордером (postOnly гарантирует maker-статус),
// и только при таймауте переходим на рыночный ордер (taker).
// Это адаптация идеи "smart entry" из исходного диалога, не
// дословный перенос кода.
func PlaceMakerOrder(ctx context.Context, side string, size, price float64, timeout time.Duration) (OrderResult, error) {
	// TODO: реализация зависит от конкретного клиента Gate.io Futures API,
	// который ещё не выбран/не написан для этого сервиса.
	return OrderResult{}, nil
}
```

Целевая доля maker-ордеров, обсуждавшаяся в диалоге: 70-80% от общего
числа сделок. Это ориентир, не жёсткое требование — экстренные
реверсы (`DecisionForceCloseHedge` и подобные) оправданно исполнять
как taker, жертвуя комиссией ради скорости.

---

## 📝 Итоговая сводка: что уже решено, что открыто

### Решено окончательно (можно закладывать в код без дополнительных вопросов)

- Философия: без тейк-профитов, закрытие только по симметричным сигналам
- Формула `ROC + Volume Power` (веса 0.6/0.4)
- Формула `VWAP Signal` (веса 0.4/0.4/0.2)
- Состав индикаторов HTF/MTF/LTF для v3.0 и их веса
- Полный процесс LONG→SHORT реверса (детекция → хедж → управление)
- Формула `CalculateHedgeSize` (общая, не тактико-специфичная)
- Логика `FundingRateManager` и `LeverageManager`
- Тактики "агрессивный" и "консервативный" реверс — полностью, все параметры
- Скальперская тактика: начальный хедж 25-30%

### Открыто, требует отдельного разговора до реализации в коде

1. **Таймфреймы 24m/6m (документ) vs 1m/8m/24m (боевой код)** — самый
   важный и приоритетный вопрос, по решению автора обсуждается отдельно
2. Симметричный сценарий SHORT→LONG (детали не проговорены)
3. Скальперская тактика: четвёртое условие, тайминг подтверждения,
   порог Net Exposure для полного реверса
4. Единая модель риск-менеджмента для одновременных LONG+SHORT
   позиций (4 нерешённых пункта, см. раздел Risk-Management)
5. `reversalConfirmed()` — точный критерий подтверждения в
   `ManageHedgedPosition`, сейчас частично покрыт порогом NetExposure
6. Диапазоны тестирования для параметров, введённых только в v3.0
   (confidence boost, volatility threshold, max leverage, max hedge duration)
7. Выбор конкретного клиента Gate.io Futures API для Go — реализация
   maker/taker orderPlacement ещё не начата

---

_Восстановлено и дописано 2026-09-03 на основе утерянного оригинала
(v1.0, DeepSeek AI) и диалога его эволюции до v3.0. Код переведён с
первоначального замысла на Node.js/TypeScript на Go — реальный стек
`dtrader-6`._

**Disclaimer:** торговля на финансовых рынках связана с риском потери
капитала. Открытые вопросы этого документа (раздел выше) должны быть
закрыты, а стратегия — протестирована на исторических данных, прежде
чем что-либо из описанного здесь получит доступ к реальным ордерам.
