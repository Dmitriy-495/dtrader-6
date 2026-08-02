package indicator

// PressureConfig — настройки расчёта P. Depth — сколько верхних уровней
// стакана (ближайших к цене) участвует в расчёте. Согласовано значение
// 20 — совпадает с orderbook.depth в bot/config.yaml, так что analyzer
// не "додумывает" глубину сверх того, что вообще публикует bot.
//
// НАМЕРЕННО не включаем сюда детекцию поглощения крупных ордеров —
// это требует истории стакана во времени (серии снапшотов), а не
// одного среза, и отложено на следующую итерацию (см. решение в
// проектировании: v1 analyzer — только bid_vol/ask_vol на N уровнях).
type PressureConfig struct {
	Depth int
}

// OBLevel — один уровень стакана: цена и объём. Совпадает по смыслу с
// gateway.OBLevel в bot (json-теги "p"/"s"), но это НЕ тот же тип —
// analyzer читает уже сериализованный JSON из Redis самостоятельно
// (см. reader/orderbook.go), поэтому здесь используются человекочитаемые
// имена полей, подходящие для внутренней структуры analyzer, а не для
// протокола Gate.io.
type OBLevel struct {
	Price float64
	Size  float64
}

// PressureSnapshot — результат расчёта P. В отличие от T и V, P не
// разбит по таймфреймам — давление в стакане это мгновенный снимок
// текущего состояния рынка, а не что-то, что агрегируется по времени.
// Публикуется в indicators:pressure:{symbol} (без {tf} в ключе).
type PressureSnapshot struct {
	BidVol float64 `json:"bid_vol"`
	AskVol float64 `json:"ask_vol"`

	// Imbalance — отношение BidVol/AskVol, согласованная формула:
	// Buy_Pressure = Σbid_vol(N уровней) / Σask_vol(N уровней).
	// >1 значит покупатели преобладают в стакане, <1 — продавцы.
	// Порог для решения (напр. >1.5 для LONG) — уже зона signal-engine,
	// не analyzer, см. согласованную границу ответственности сервисов.
	Imbalance float64 `json:"imbalance"`

	Ts int64 `json:"ts"`
}

// CalcPressure считает P по срезам уровней bid/ask. bids/asks должны
// быть уже отсортированы от лучшей цены к худшей и обрезаны до
// cfg.Depth уровней вызывающим кодом (reader/orderbook.go) — сама
// функция не сортирует и не обрезает, чтобы оставаться простой чистой
// функцией без побочных предположений о происхождении данных.
//
// ВАЖНО (см. проектирование): на момент написания market:orderbook в
// Redis содержит инкрементальную дельту стакана, а не полный снапшот —
// bot дорабатывается параллельно, чтобы отдавать полный снапшот. Эта
// функция уже готова принять корректные полные bids/asks, как только
// reader/orderbook.go получит их из Redis.
func CalcPressure(bids, asks []OBLevel, ts int64) PressureSnapshot {
	snap := PressureSnapshot{Ts: ts}

	for _, lvl := range bids {
		snap.BidVol += lvl.Size
	}
	for _, lvl := range asks {
		snap.AskVol += lvl.Size
	}

	if snap.AskVol > 0 {
		snap.Imbalance = snap.BidVol / snap.AskVol
	}
	return snap
}
