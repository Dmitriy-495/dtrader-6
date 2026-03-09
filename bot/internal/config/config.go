// Пакет config отвечает за загрузку и хранение всей конфигурации бота.
// Читает config.yaml (основные настройки) и .env (секреты),
// собирает всё в единую структуру Config.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"go.yaml.in/yaml/v3"
)

// AppConfig — секция app: в config.yaml
type AppConfig struct {
	// Name — имя сервиса, используется в логах
	Name string `yaml:"name"`
	// Env — окружение: development | production
	Env string `yaml:"env"`
}

// ExchangeConfig — секция exchange: в config.yaml
type ExchangeConfig struct {
	// Name — название биржи
	Name string `yaml:"name"`
	// WsURL — WebSocket URL USDT Perpetual Futures
	WsURL string `yaml:"ws_url"`
	// WsTestnetURL — WebSocket URL тестовой сети
	WsTestnetURL string `yaml:"ws_testnet_url"`
	// RestURL — базовый URL REST API
	RestURL string `yaml:"rest_url"`
	// ReconnectInterval — пауза перед переподключением при разрыве WS.
	// TODO: распарсить в time.Duration при реализации gateway/ws.go
	ReconnectInterval string `yaml:"reconnect_interval"`
	// PingInterval — интервал ping/pong для поддержания WS соединения.
	// TODO: распарсить в time.Duration при реализации gateway/ws.go
	PingInterval string `yaml:"ping_interval"`
}

// OrderbookConfig — секция orderbook: в config.yaml
type OrderbookConfig struct {
	// Depth — глубина стакана (количество уровней bid и ask)
	Depth int `yaml:"depth"`
}

// RedisConfig — секция redis: в config.yaml
type RedisConfig struct {
	// Host — адрес Redis сервера
	Host string `yaml:"host"`
	// Port — порт Redis сервера (по умолчанию 6379)
	Port int `yaml:"port"`
	// DB — номер базы данных Redis (0-15)
	DB int `yaml:"db"`
	// Password — загружается из .env (REDIS_PASSWORD), не из yaml
	Password string
}

// StorageConfig — секция storage: в config.yaml
// Определяет размеры скользящих окон данных в Redis (LTRIM).
type StorageConfig struct {
	// Candles1m — количество хранимых 1m свечей (~3 часа при 200 свечах)
	Candles1m int `yaml:"candles_1m"`
	// Trades — количество хранимых тиков на символ
	Trades int `yaml:"trades"`
}

// SecretsConfig — секреты из .env файла.
// Никогда не хранятся в config.yaml — только в .env!
type SecretsConfig struct {
	// APIKey — Gate.io API ключ (GATE_API_KEY в .env)
	APIKey string
	// APISecret — Gate.io API Secret (GATE_API_SECRET в .env)
	APISecret string
}

// Config — главная структура конфигурации.
// Единственный источник всех настроек бота.
// Создаётся один раз в main() и передаётся во все модули.
type Config struct {
	App       AppConfig      `yaml:"app"`
	Exchange  ExchangeConfig `yaml:"exchange"`
	Symbols   []string       `yaml:"symbols"`
	Orderbook OrderbookConfig `yaml:"orderbook"`
	Redis     RedisConfig    `yaml:"redis"`
	Storage   StorageConfig  `yaml:"storage"`
	// Secrets не имеет тега yaml — заполняется из переменных окружения.
	Secrets SecretsConfig
}

// Load загружает конфигурацию из config.yaml и .env.
//
// Порядок загрузки:
//  1. Загружаем .env → переменные окружения
//  2. Читаем config.yaml → основные настройки
//  3. Читаем переменные окружения → секреты
//  4. Валидируем все критичные поля
func Load(configPath string) (*Config, error) {
	// ШАГ 1: Загружаем .env.
	// Ошибку игнорируем — на VDS секреты задаются системно.
	_ = godotenv.Load()

	// ШАГ 2: Открываем config.yaml.
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть config.yaml: %w", err)
	}
	defer file.Close()

	// ШАГ 3: Парсим YAML → Config.
	// &cfg — передаём адрес, декодер пишет напрямую в память.
	var cfg Config
	if err := yaml.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("не удалось разобрать config.yaml: %w", err)
	}

	// ШАГ 4: Загружаем секреты из переменных окружения.
	cfg.Secrets.APIKey = os.Getenv("GATE_API_KEY")
	cfg.Secrets.APISecret = os.Getenv("GATE_API_SECRET")
	cfg.Redis.Password = os.Getenv("REDIS_PASSWORD")

	// ШАГ 5: Валидация — падаем здесь с понятной ошибкой
	// вместо загадочного сбоя глубже в коде.
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate проверяет все критичные поля конфигурации.
// Приватный метод — вызывается только из Load.
// Вынесен отдельно чтобы не загромождать Load.
func (c *Config) validate() error {
	// Секреты
	if c.Secrets.APIKey == "" {
		return fmt.Errorf("GATE_API_KEY не задан в .env файле")
	}
	if c.Secrets.APISecret == "" {
		return fmt.Errorf("GATE_API_SECRET не задан в .env файле")
	}

	// Биржа
	if c.Exchange.RestURL == "" {
		return fmt.Errorf("exchange.rest_url не задан в config.yaml")
	}
	if c.Exchange.WsURL == "" {
		return fmt.Errorf("exchange.ws_url не задан в config.yaml")
	}

	// Символы
	if len(c.Symbols) == 0 {
		return fmt.Errorf("symbols не заданы в config.yaml")
	}

	return nil
}
