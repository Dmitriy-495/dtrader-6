package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"go.yaml.in/yaml/v3"
)

type AppConfig struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	DB       int    `yaml:"db"`
	Password string
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type SecretsConfig struct {
	APIKey string
}

type Config struct {
	App     AppConfig    `yaml:"app"`
	Redis   RedisConfig  `yaml:"redis"`
	Server  ServerConfig `yaml:"server"`
	Symbols []string     `yaml:"symbols"`
	Secrets SecretsConfig
}

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

	cfg.Secrets.APIKey = os.Getenv("WS_API_KEY")
	cfg.Redis.Password = os.Getenv("REDIS_PASSWORD")

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Secrets.APIKey == "" {
		return fmt.Errorf("WS_API_KEY не задан в .env файле")
	}
	if c.Server.Port == 0 {
		return fmt.Errorf("server.port не задан в config.yaml")
	}
	if len(c.Symbols) == 0 {
		return fmt.Errorf("symbols не заданы в config.yaml")
	}
	return nil
}
