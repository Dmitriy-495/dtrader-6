// Пакет reader отвечает за чтение "сырых" market:* данных из Redis и их
// превращение в форму, понятную indicator/ (срезы float64, структуры
// уровней стакана) — по аналогии с parser.go в bot, только в обратную
// сторону: там сеть → Redis, здесь Redis → indicator.
package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RawCandle — одна свеча ровно в том JSON-формате, в котором bot её
// публикует в market:candles:1m:{symbol} (см. gateway.Candle в bot).
// Поля читаем как строки (Open/Close/High/Low/Volume) — так их прислала
// биржа через bot, парсинг в float64 делаем сами при агрегации, не
// полагаясь на то, что JSON всегда будет числом без кавычек.
type RawCandle struct {
	T      int64  `json:"t"`
	Open   string `json:"o"`
	Close  string `json:"c"`
	High   string `json:"h"`
	Low    string `json:"l"`
	Volume string `json:"v"`
}

// Candle — свеча после парсинга, с числовыми полями. Используется и как
// результат чтения 1m-свечей, и как результат агрегации 8m/24m — оба
// случая нужны indicator/ в одинаковом виде (срез Close-цен и т.д.).
type Candle struct {
	T      int64
	Open   float64
	Close  float64
	High   float64
	Low    float64
	Volume float64
}

// CandleReader читает market:candles:1m:{symbol} и умеет агрегировать
// прочитанные 1m-свечи в более крупные таймфреймы.
type CandleReader struct {
	rdb *redis.Client
}

func NewCandleReader(rdb *redis.Client) *CandleReader {
	return &CandleReader{rdb: rdb}
}

// FetchRecent1m читает последние `limit` свечей market:candles:1m:{symbol}
// (List, RPUSH+LTRIM в bot — то есть хронологический порядок: индекс 0 —
// самая старая из хранимых, последний индекс — самая свежая) и парсит
// их в []Candle. limit должен быть достаточным, чтобы после агрегации
// в самый крупный настроенный ТФ (например 24m) осталось нужное число
// точек для TrendConfig этого ТФ (EMA/RSI/Angle периодов) — это считает
// вызывающий код в engine/, не reader.
func (r *CandleReader) FetchRecent1m(ctx context.Context, symbol string, limit int64) ([]Candle, error) {
	key := fmt.Sprintf("market:candles:1m:%s", symbol)
	// LRANGE -limit -1 берёт последние limit элементов списка — то есть
	// самые свежие свечи, а не самые старые, что нам и нужно для
	// расчёта индикаторов "по состоянию на сейчас".
	raw, err := r.rdb.LRange(ctx, key, -limit, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("LRange %s: %w", key, err)
	}

	candles := make([]Candle, 0, len(raw))
	for _, item := range raw {
		var rc RawCandle
		if err := json.Unmarshal([]byte(item), &rc); err != nil {
			// Одна повреждённая запись не должна валить весь расчёт —
			// логируем и пропускаем, как и parser.go в bot пропускает
			// отдельные некорректные сообщения, не обрывая ReadLoop.
			log.Printf("⚠️ reader candles %s: пропущена повреждённая запись: %v", symbol, err)
			continue
		}
		candle, err := parseRawCandle(rc)
		if err != nil {
			log.Printf("⚠️ reader candles %s: %v", symbol, err)
			continue
		}
		candles = append(candles, candle)
	}
	return candles, nil
}

func parseRawCandle(rc RawCandle) (Candle, error) {
	open, err := strconv.ParseFloat(rc.Open, 64)
	if err != nil {
		return Candle{}, fmt.Errorf("open %q: %w", rc.Open, err)
	}
	closeP, err := strconv.ParseFloat(rc.Close, 64)
	if err != nil {
		return Candle{}, fmt.Errorf("close %q: %w", rc.Close, err)
	}
	high, err := strconv.ParseFloat(rc.High, 64)
	if err != nil {
		return Candle{}, fmt.Errorf("high %q: %w", rc.High, err)
	}
	low, err := strconv.ParseFloat(rc.Low, 64)
	if err != nil {
		return Candle{}, fmt.Errorf("low %q: %w", rc.Low, err)
	}
	volume, err := strconv.ParseFloat(rc.Volume, 64)
	if err != nil {
		return Candle{}, fmt.Errorf("volume %q: %w", rc.Volume, err)
	}
	return Candle{T: rc.T, Open: open, Close: closeP, High: high, Low: low, Volume: volume}, nil
}

// Aggregate объединяет минутные свечи в свечи более крупного таймфрейма.
// minutes — сколько исходных 1m-свечей формируют одну выходную свечу
// (8 для "8m", 24 для "24m" — см. config.AggregateConfig.Minutes).
//
// Группировка идёт с КОНЦА среза (от самых свежих данных назад) — это
// значит, что если len(oneMin) не делится нацело на minutes, "неполный"
// остаток окажется в начале результата (самая старая, скорее всего
// недостроенная группа), а не в конце. Это важно: последняя (самая
// свежая) агрегированная свеча должна быть полной группой из minutes
// исходных свечей, иначе индикаторы на старшем ТФ будут считаться по
// незакрытой, вводящей в заблуждение "свече".
func Aggregate(oneMin []Candle, minutes int) []Candle {
	if minutes <= 0 || len(oneMin) == 0 {
		return nil
	}

	n := len(oneMin) / minutes
	if n == 0 {
		return nil
	}

	// Отбрасываем "хвост" в начале среза, который не составляет полную
	// группу — см. комментарий выше про группировку с конца.
	start := len(oneMin) - n*minutes
	result := make([]Candle, 0, n)

	for i := start; i < len(oneMin); i += minutes {
		group := oneMin[i : i+minutes]
		result = append(result, mergeCandles(group))
	}
	return result
}

// mergeCandles сворачивает группу минутных свечей в одну свечу большего
// таймфрейма по стандартным правилам OHLCV rollup: Open группы = Open
// первой свечи, Close = Close последней, High/Low = максимум/минимум по
// всей группе, Volume = сумма.
func mergeCandles(group []Candle) Candle {
	merged := Candle{
		T:      group[0].T,
		Open:   group[0].Open,
		Close:  group[len(group)-1].Close,
		High:   group[0].High,
		Low:    group[0].Low,
		Volume: 0,
	}
	for _, c := range group {
		if c.High > merged.High {
			merged.High = c.High
		}
		if c.Low < merged.Low {
			merged.Low = c.Low
		}
		merged.Volume += c.Volume
	}
	return merged
}

// ClosePrices — вспомогательная функция для indicator/: извлекает срез
// цен закрытия из среза свечей, в хронологическом порядке. Большинство
// T-индикаторов (EMA, RSI, MACD, TrendAngle) работают именно с ценами
// закрытия, поэтому это общее место, а не дублируется в каждом вызове.
func ClosePrices(candles []Candle) []float64 {
	prices := make([]float64, len(candles))
	for i, c := range candles {
		prices[i] = c.Close
	}
	return prices
}

// NowMs — миллисекундный unix-timestamp текущего момента. Небольшая
// локальная утилита вместо отдельного пакета utils/time.go (как в bot)
// — единственное место в analyzer, где это пока нужно; если понадобится
// больше похожих хелперов, тогда есть смысл выделить общий пакет.
func NowMs() int64 {
	return time.Now().UnixMilli()
}
