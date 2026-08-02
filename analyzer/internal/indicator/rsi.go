package indicator

// RSI — индекс относительной силы (Relative Strength Index), классический
// осциллятор 0-100. В стратегии TVP используется как ВСПОМОГАТЕЛЬНЫЙ
// фильтр на 24m ("Главнокомандующий") — не основной генератор сигнала
// (это T-тренд через EMA), а дополнительное подтверждение, что рынок не
// находится в состоянии крайней перекупленности/перепроданности перед
// разрешением на вход.
//
// period — стандартно 14 (RSI(14) из TVP_SNIPER.md, номинальное значение).
//
// prices — цены закрытия в хронологическом порядке. Нужно минимум
// period+1 значений, чтобы посчитать period разностей между ценами.
//
// Возвращает 0, если данных недостаточно (len(prices) <= period) —
// как и в EMA, 0 нужно трактовать как "ещё нет значения", не как
// настоящий RSI=0 (что означало бы абсолютно однонаправленное падение).
func RSI(prices []float64, period int) float64 {
	if period <= 0 || len(prices) <= period {
		return 0
	}

	// Классический RSI по Уайлдеру: считаем средний прирост и средние
	// потери за period последних изменений цены, затем RS = avgGain/avgLoss,
	// RSI = 100 - 100/(1+RS).
	var gainSum, lossSum float64
	start := len(prices) - period - 1
	for i := start + 1; i < len(prices); i++ {
		delta := prices[i] - prices[i-1]
		if delta > 0 {
			gainSum += delta
		} else {
			lossSum += -delta
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	// Если за весь период не было ни одного падения — рынок в чистом
	// восходящем движении, RSI = 100 (предел индикатора, не деление на 0).
	if avgLoss == 0 {
		if avgGain == 0 {
			// Цена вообще не менялась — нейтральное значение.
			return 50
		}
		return 100
	}

	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}
