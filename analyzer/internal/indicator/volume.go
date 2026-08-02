package indicator

// VolumeConfig — настройки расчёта V. SpikeMultiplier/SMAPeriod относятся
// к детекции объёмного всплеска (Volume Spike из TVP_SNIPER.md,
// номинально ×2.5 от SMA(20)) — см. VolumeSnapshot.Spike ниже.
type VolumeConfig struct {
	SpikeMultiplier float64 // напр. 2.5
	SMAPeriod       int     // напр. 20 — сколько последних окон брать в среднее
}

// VolumeSnapshot — результат расчёта V за одно окно наблюдения (окно
// соответствует таймфрейму: для indicators:volume:1m — последняя минута,
// для indicators:volume:8m — последние 8 минут и т.д.). Публикуется в
// indicators:volume:{tf}:{symbol}.
type VolumeSnapshot struct {
	BuyVol  float64 `json:"buy_vol"`
	SellVol float64 `json:"sell_vol"`

	// Delta — чистое объёмное давление за окно: BuyVol - SellVol.
	// Положительная — покупатели агрессивнее, отрицательная — продавцы.
	Delta float64 `json:"delta"`

	// Spike — сработал ли детектор объёмного всплеска: суммарный объём
	// окна (BuyVol+SellVol) превысил SMA предыдущих окон в SpikeMultiplier
	// раз. true означает "это движение подтверждено необычно высоким
	// объёмом" — именно то, что TVP_SNIPER.md требует как подтверждение
	// импульса на 8m ("Генерал").
	Spike bool `json:"spike"`

	Ts int64 `json:"ts"`
}

// CalcVolume считает V за текущее окно (buyVol/sellVol уже просуммированы
// вызывающим кодом в reader/trades.go по всем сделкам за окно) и
// сравнивает суммарный объём с историей прошлых окон (recentTotals —
// суммарные объёмы предыдущих завершённых окон, старые → новые, обычно
// длиной cfg.SMAPeriod) для детекции всплеска.
func CalcVolume(cfg VolumeConfig, buyVol, sellVol float64, recentTotals []float64, ts int64) VolumeSnapshot {
	snap := VolumeSnapshot{
		BuyVol:  buyVol,
		SellVol: sellVol,
		Delta:   buyVol - sellVol,
		Ts:      ts,
	}

	if len(recentTotals) == 0 || cfg.SpikeMultiplier <= 0 {
		return snap
	}

	var sum float64
	for _, v := range recentTotals {
		sum += v
	}
	sma := sum / float64(len(recentTotals))
	if sma <= 0 {
		return snap
	}

	currentTotal := buyVol + sellVol
	snap.Spike = currentTotal > sma*cfg.SpikeMultiplier
	return snap
}
