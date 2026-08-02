package indicator

import "math"

// TrendAngle считает угол наклона тренда через простую линейную регрессию
// цены по индексу времени (0, 1, 2, ... len(prices)-1), возвращает угол
// в градусах.
//
// Это ровно та реализация, что была согласована на основе TVP_SNIPER.md
// (раздел "Trend Angle Calculation") — угол наклона, а не просто разница
// цен: линейная регрессия сглаживает шум одиночных свечей лучше, чем
// наивное (price_current - price_n_periods_ago) / n, что и обсуждалось
// как более надёжный вариант при разработке стратегии.
//
// Положительный угол — восходящий тренд, отрицательный — нисходящий,
// чем больше |angle|, тем круче движение цены. periods обычно = 20
// (номинальное значение из TVP_SNIPER.md, диапазон тестирования 10-25
// относится к порогу ПРИНЯТИЯ РЕШЕНИЯ по углу в signal-engine, а не к
// периоду расчёта здесь — сам период расчёта фиксирован конфигом).
//
// Возвращает 0, если данных меньше 2 точек (регрессия по одной точке
// не определена).
func TrendAngle(prices []float64) float64 {
	n := len(prices)
	if n < 2 {
		return 0
	}

	// Классическая формула наименьших квадратов для slope:
	// slope = (n*ΣXY - ΣX*ΣY) / (n*ΣX² - (ΣX)²)
	// где X — индексы 0..n-1, Y — цены.
	var sumX, sumY, sumXY, sumX2 float64
	for i, price := range prices {
		x := float64(i)
		sumX += x
		sumY += price
		sumXY += x * price
		sumX2 += x * x
	}
	nf := float64(n)
	denominator := nf*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}
	slope := (nf*sumXY - sumX*sumY) / denominator

	// atan(slope) даёт угол в радианах между линией регрессии и
	// горизонталью; переводим в градусы для читаемости в indicators:*
	// и для прямого сравнения с порогами из TVP_SNIPER.md (10°-25°).
	return math.Atan(slope) * (180 / math.Pi)
}
