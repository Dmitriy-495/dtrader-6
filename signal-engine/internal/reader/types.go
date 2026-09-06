// Файл types.go описывает структуры T/V/P снапшотов ровно в том виде,
// в котором их публикует analyzer (см. analyzer/internal/indicator и
// analyzer/internal/publisher). signal-engine НЕ импортирует пакет
// analyzer напрямую (разные Go-модули, разные бинарники, независимый
// деплой) — вместо этого здесь заведена собственная копия JSON-схемы,
// которую нужно держать синхронной с analyzer вручную. Если формат
// когда-нибудь разъедется, unit-тесты reader на реальных прод-снапшотах
// (см. reader_test.go) должны об этом сообщить первыми.
package reader

// Direction — направление тренда. Зеркалирует indicator.Direction
// из analyzer, как строку "up"/"down"/"neutral".
type Direction string

const (
	DirectionUp      Direction = "up"
	DirectionDown    Direction = "down"
	DirectionNeutral Direction = "neutral"
)

// TrendSnapshot — T на одном таймфрейме для одного символа.
// Источник: indicators:trend:{tf}:{symbol}.
type TrendSnapshot struct {
	EMAFast   float64   `json:"ema_fast"`
	EMASlow   float64   `json:"ema_slow"`
	Direction Direction `json:"direction"`

	// Angle/RSI/MACDHistogram — 0, если соответствующий расчёт не
	// включён на этом ТФ в analyzer (см. TrendConfig.UseRSI/UseMACD/
	// AnglePeriods в analyzer/internal/indicator/trend.go). signal-engine
	// не может по одному только числу 0 отличить "не считалось" от
	// "посчитано и ровно 0" — при написании rules/ это нужно учитывать
	// явно (например через собственную конфигурацию таймфреймов), а не
	// молча доверять нулю как значимому результату.
	Angle         float64 `json:"angle"`
	RSI           float64 `json:"rsi"`
	MACDHistogram float64 `json:"macd_histogram"`

	Ts int64 `json:"ts"`
}

// VolumeSnapshot — V на одном таймфрейме для одного символа.
// Источник: indicators:volume:{tf}:{symbol}.
type VolumeSnapshot struct {
	BuyVol  float64 `json:"buy_vol"`
	SellVol float64 `json:"sell_vol"`
	Delta   float64 `json:"delta"`
	Spike   bool    `json:"spike"`
	Ts      int64   `json:"ts"`
}

// PressureSnapshot — P для одного символа, без привязки к таймфрейму.
// Источник: indicators:pressure:{symbol}.
type PressureSnapshot struct {
	BidVol    float64 `json:"bid_vol"`
	AskVol    float64 `json:"ask_vol"`
	Imbalance float64 `json:"imbalance"`
	Ts        int64   `json:"ts"`
}
