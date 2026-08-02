// Пакет config отвечает за загрузку и хранение всей конфигурации analyzer.
// Читает config.yaml (какие символы, таймфреймы, периоды индикаторов) и .env
// (пароль Redis — единственный секрет, который нужен analyzer: он read-only
// потребитель рыночных данных и никогда не ходит к Gate.io напрямую).
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.yaml.in/yaml/v3"
)

// AppConfig — секция app: в config.yaml
type AppConfig struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

// RedisConfig — секция redis: в config.yaml.
// Password не хранится в YAML — только в .env (REDIS_PASSWORD).
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	DB       int    `yaml:"db"`
	Password string
}

// TrendParams — периоды индикаторов тренда (T) для одного таймфрейма.
// MACDFast/MACDSlow/MACDSignal равны 0 на таймфреймах, где MACD не считается
// (см. config.yaml: MACD сейчас настроен только для 8m) — indicator/macd.go
// должен явно проверять это перед расчётом, а не полагаться на "0 как валидное
// значение периода".
type TrendParams struct {
	EMAFast      int `yaml:"ema_fast"`
	EMASlow      int `yaml:"ema_slow"`
	RSIPeriod    int `yaml:"rsi_period"`
	AnglePeriods int `yaml:"angle_periods"`
	MACDFast     int `yaml:"macd_fast"`
	MACDSlow     int `yaml:"macd_slow"`
	MACDSignal   int `yaml:"macd_signal"`
}

// VolumeParams — секция indicators.volume: в config.yaml.
// Общие для всех ТФ на первом этапе — если понадобится разделить
// по таймфреймам, расширяем аналогично TrendParams (map[string]VolumeParams).
type VolumeParams struct {
	SpikeMultiplier float64 `yaml:"spike_multiplier"`
	SMAPeriod       int     `yaml:"sma_period"`
}

// PressureParams — секция indicators.pressure: в config.yaml.
type PressureParams struct {
	// Depth — сколько уровней стакана (bid/ask) участвует в расчёте
	// Buy_Pressure = Σbid_vol(Depth) / Σask_vol(Depth).
	Depth int `yaml:"depth"`
}

// IndicatorsConfig — секция indicators: в config.yaml.
// Trend — map по таймфрейму ("1m"/"8m"/"24m"), потому что периоды
// принципиально разные на каждом ТФ (см. TVP_SNIPER: EMA(72) на 24m,
// EMA(24) на 8m). Volume и Pressure пока общие для всех ТФ.
type IndicatorsConfig struct {
	Trend    map[string]TrendParams `yaml:"trend"`
	Volume   VolumeParams           `yaml:"volume"`
	Pressure PressureParams         `yaml:"pressure"`
}

// TimeframesConfig — секция timeframes: в config.yaml.
type TimeframesConfig struct {
	// Base — нативный таймфрейм, приходящий из bot (market:candles:1m:*).
	// Всегда "1m" на практике, но не хардкодим — явное значение из
	// конфига честнее и не удивит через полгода при чтении кода.
	Base string `yaml:"base"`
	// Aggregates — таймфреймы, которые analyzer строит сам из Base
	// (например "8m", "24m"). Каждый элемент должен быть кратен Base
	// в минутах — проверяется в validate().
	Aggregates []string `yaml:"aggregates"`
}

// Config — главная структура конфигурации analyzer.
type Config struct {
	App        AppConfig        `yaml:"app"`
	Symbols    []string         `yaml:"symbols"`
	Redis      RedisConfig      `yaml:"redis"`
	Timeframes TimeframesConfig `yaml:"timeframes"`
	Indicators IndicatorsConfig `yaml:"indicators"`
	// CalcInterval — как часто пересчитывать индикаторы из накопленного
	// состояния (строка вида "5s" в YAML), см. CalcIntervalDuration().
	CalcInterval string `yaml:"calc_interval"`

	// calcIntervalDur — распарсенное значение CalcInterval. Приватное,
	// заполняется в parseDurations() сразу после чтения YAML — тот же
	// принцип, что и в bot/internal/config (см. ReconnectInterval там):
	// разбор строки в Duration происходит один раз, при старте, а не
	// разбросан по местам использования.
	calcIntervalDur time.Duration
}

// CalcIntervalDuration возвращает CalcInterval как time.Duration,
// готовую к использованию в time.NewTicker() и подобных функциях.
func (c Config) CalcIntervalDuration() time.Duration {
	return c.calcIntervalDur
}

// AllTimeframes возвращает Base + Aggregates одним срезом — удобно для
// мест, где нужно пройтись по всем таймфреймам разом (например при
// инициализации буферов состояния в engine).
func (c Config) AllTimeframes() []string {
	result := make([]string, 0, len(c.Timeframes.Aggregates)+1)
	result = append(result, c.Timeframes.Base)
	result = append(result, c.Timeframes.Aggregates...)
	return result
}

// Load загружает конфигурацию из config.yaml и .env.
//
// Порядок:
//  1. .env → переменные окружения (REDIS_PASSWORD)
//  2. config.yaml → основные настройки
//  3. Парсинг calc_interval в time.Duration
//  4. Секреты из окружения
//  5. Валидация
func Load(configPath string) (*Config, error) {
	_ = godotenv.Load()

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть config.yaml: %w", err)
	}
	defer file.Close()

	var cfg Config
	if err := yaml.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("не удалось разобрать config.yaml: %w", err)
	}

	if err := cfg.parseDurations(); err != nil {
		return nil, err
	}

	cfg.Redis.Password = os.Getenv("REDIS_PASSWORD")

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) parseDurations() error {
	dur, err := time.ParseDuration(c.CalcInterval)
	if err != nil {
		return fmt.Errorf("calc_interval некорректен (%q): %w", c.CalcInterval, err)
	}
	c.calcIntervalDur = dur
	return nil
}

// validate проверяет все критичные поля конфигурации analyzer.
// Падаем на старте с понятной ошибкой — так же, как это сделано в
// bot/internal/config: лучше не запуститься вовсе, чем считать индикаторы
// по молча-неверной конфигурации (например с нулевым периодом EMA).
func (c *Config) validate() error {
	if len(c.Symbols) == 0 {
		return fmt.Errorf("symbols не заданы в config.yaml")
	}

	if c.Timeframes.Base == "" {
		return fmt.Errorf("timeframes.base не задан в config.yaml")
	}
	if len(c.Timeframes.Aggregates) == 0 {
		return fmt.Errorf("timeframes.aggregates не заданы в config.yaml")
	}

	// Периоды индикаторов тренда должны быть заданы для КАЖДОГО
	// таймфрейма из AllTimeframes() — иначе engine наткнётся на
	// отсутствующий ключ в map и либо упадёт в рантайме, либо (что хуже)
	// молча посчитает с нулевыми периодами.
	for _, tf := range c.AllTimeframes() {
		params, ok := c.Indicators.Trend[tf]
		if !ok {
			return fmt.Errorf("indicators.trend.%s не задан в config.yaml", tf)
		}
		if params.EMAFast <= 0 || params.EMASlow <= 0 {
			return fmt.Errorf("indicators.trend.%s: ema_fast/ema_slow должны быть положительными", tf)
		}
		if params.EMAFast >= params.EMASlow {
			return fmt.Errorf("indicators.trend.%s: ema_fast (%d) должен быть меньше ema_slow (%d)",
				tf, params.EMAFast, params.EMASlow)
		}
	}

	if c.Indicators.Volume.SpikeMultiplier <= 0 {
		return fmt.Errorf("indicators.volume.spike_multiplier должен быть положительным")
	}
	if c.Indicators.Volume.SMAPeriod <= 0 {
		return fmt.Errorf("indicators.volume.sma_period должен быть положительным")
	}

	if c.Indicators.Pressure.Depth <= 0 {
		return fmt.Errorf("indicators.pressure.depth должен быть положительным")
	}

	if c.Redis.Host == "" {
		return fmt.Errorf("redis.host не задан в config.yaml")
	}

	return nil
}
