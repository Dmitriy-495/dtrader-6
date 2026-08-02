package indicator

// MACDResult — результат расчёта MACD: линия MACD, сигнальная линия и
// гистограмма (разница между ними). В TVP_SNIPER.md на MTF(8m) как раз
// используется "MACD Signal: Гистограмма > 0" — то есть из всего MACD
// нам в первую очередь важен знак Histogram, но отдаём все три значения,
// чтобы signal-engine мог применить и более тонкую логику при желании
// (например скорость роста гистограммы), не только знак.
type MACDResult struct {
	MACD      float64 // EMA(fast) - EMA(slow)
	Signal    float64 // EMA(signalPeriod) от линии MACD
	Histogram float64 // MACD - Signal
}

// MACD считает классический Moving Average Convergence Divergence.
//
// prices — цены закрытия в хронологическом порядке.
// fastPeriod/slowPeriod — периоды двух EMA для линии MACD (стандартно 12/26).
// signalPeriod — период EMA сигнальной линии (стандартно 9).
//
// Возвращает нулевой MACDResult, если данных недостаточно для построения
// хотя бы одного значения EMA(slowPeriod) — то есть len(prices) == 0.
// В отличие от EMA/RSI, MACD не требует len(prices) > period для того
// чтобы дать хоть какое-то (пусть шумное на старте) значение — EMA сама
// по себе определена и на короткой серии, просто менее точна, пока не
// накопится история порядка нескольких periods. Решение сглаживать это
// требование оставлено вызывающему коду (trend.go), который отслеживает,
// сколько данных накоплено, прежде чем публиковать результат.
func MACD(prices []float64, fastPeriod, slowPeriod, signalPeriod int) MACDResult {
	if len(prices) == 0 {
		return MACDResult{}
	}

	fastSeries := EMASeries(prices, fastPeriod)
	slowSeries := EMASeries(prices, slowPeriod)

	// Линия MACD на каждом шаге — разница двух EMA-серий одинаковой длины.
	macdSeries := make([]float64, len(prices))
	for i := range prices {
		macdSeries[i] = fastSeries[i] - slowSeries[i]
	}

	// Сигнальная линия — EMA(signalPeriod) уже от самой линии MACD,
	// а не от исходных цен.
	signalSeries := EMASeries(macdSeries, signalPeriod)

	lastIdx := len(prices) - 1
	macd := macdSeries[lastIdx]
	signal := signalSeries[lastIdx]

	return MACDResult{
		MACD:      macd,
		Signal:    signal,
		Histogram: macd - signal,
	}
}
