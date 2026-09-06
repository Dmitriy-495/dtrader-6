// Пакет config отвечает за загрузку и хранение всей конфигурации
// signal-engine. Читает config.yaml (какие символы, таймфреймы, как
// часто опрашивать Redis) и .env (пароль Redis — единственный секрет,
// который нужен signal-engine: он read-only потребитель indicators:*
// и никогда не ходит к Gate.io напрямую).
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

// Config — главная структура конфигурации signal-engine.
//
// RulesRaw хранит секцию rules: как есть (map[string]any), потому что
// на момент написания этого кода точная методология сборки T+V+P в
// сигнал (TVP_SNIPER.md) отсутствует — см. комментарий в config.yaml.
// Как только методология будет восстановлена с автором, эта секция
// должна получить собственный типизированный RulesConfig (по аналогии
// с analyzer/internal/config.IndicatorsConfig), а не оставаться
// map[string]any навсегда.
type Config struct {
	App     AppConfig `yaml:"app"`
	Symbols []string  `yaml:"symbols"`
	Redis   RedisConfig `yaml:"redis"`

	// Timeframes — таймфреймы, которые signal-engine ожидает найти в
	// indicators:trend:{tf}:{symbol} / indicators:volume:{tf}:{symbol}.
	// Должны совпадать с analyzer/config.yaml (timeframes.base +
	// timeframes.aggregates) — не проверяется автоматически между
	// сервисами, только validate()'ом здесь на непустоту.
	Timeframes []string `yaml:"timeframes"`

	// PollInterval — как часто опрашивать indicators:* в Redis
	// (строка вида "5s" в YAML), см. PollIntervalDuration().
	PollInterval string `yaml:"poll_interval"`

	// StalenessThreshold — максимально допустимое отставание поля ts
	// внутри JSON от текущего момента (строка вида "20s" в YAML),
	// см. StalenessThresholdDuration().
	StalenessThreshold string `yaml:"staleness_threshold"`

	// RulesRaw — секция rules: как есть, см. комментарий у Config выше.
	RulesRaw map[string]any `yaml:"rules"`

	pollIntervalDur     time.Duration
	stalenessThresholdD time.Duration
}

// PollIntervalDuration возвращает PollInterval как time.Duration.
func (c Config) PollIntervalDuration() time.Duration {
	return c.pollIntervalDur
}

// StalenessThresholdDuration возвращает StalenessThreshold как time.Duration.
func (c Config) StalenessThresholdDuration() time.Duration {
	return c.stalenessThresholdD
}

// Load загружает конфигурацию из config.yaml и .env.
//
// Порядок:
//  1. .env → переменные окружения (REDIS_PASSWORD)
//  2. config.yaml → основные настройки
//  3. Парсинг poll_interval/staleness_threshold в time.Duration
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
	pollDur, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return fmt.Errorf("poll_interval некорректен (%q): %w", c.PollInterval, err)
	}
	c.pollIntervalDur = pollDur

	staleDur, err := time.ParseDuration(c.StalenessThreshold)
	if err != nil {
		return fmt.Errorf("staleness_threshold некорректен (%q): %w", c.StalenessThreshold, err)
	}
	c.stalenessThresholdD = staleDur

	return nil
}

// validate проверяет все критичные поля конфигурации signal-engine.
// Падаем на старте с понятной ошибкой — тот же принцип, что и в
// analyzer/internal/config: лучше не запуститься вовсе, чем читать
// indicators:* по молча-неверной конфигурации.
func (c *Config) validate() error {
	if len(c.Symbols) == 0 {
		return fmt.Errorf("symbols не заданы в config.yaml")
	}

	if len(c.Timeframes) == 0 {
		return fmt.Errorf("timeframes не заданы в config.yaml")
	}

	if c.pollIntervalDur <= 0 {
		return fmt.Errorf("poll_interval должен быть положительным")
	}

	if c.stalenessThresholdD <= 0 {
		return fmt.Errorf("staleness_threshold должен быть положительным")
	}

	if c.Redis.Host == "" {
		return fmt.Errorf("redis.host не задан в config.yaml")
	}

	return nil
}
