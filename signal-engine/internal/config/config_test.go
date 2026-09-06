package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTempConfig создаёт временный config.yaml с заданным содержимым
// и возвращает путь к нему. t.TempDir() сам чистит за собой.
func writeTempConfig(t *testing.T, yamlContent string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("не удалось записать временный config.yaml: %v", err)
	}
	return path
}

const validConfigYAML = `
app:
  name: dtrader-6-signal-engine-test
  env: development
symbols:
  - BTC_USDT
  - ETH_USDT
redis:
  host: localhost
  port: 6379
  db: 0
timeframes:
  - 1m
  - 8m
  - 24m
poll_interval: 5s
staleness_threshold: 20s
rules: {}
`

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTempConfig(t, validConfigYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load вернул ошибку на валидном конфиге: %v", err)
	}

	if len(cfg.Symbols) != 2 {
		t.Errorf("Symbols len = %d, хотим 2", len(cfg.Symbols))
	}
	if len(cfg.Timeframes) != 3 {
		t.Errorf("Timeframes len = %d, хотим 3", len(cfg.Timeframes))
	}
	if cfg.PollIntervalDuration() != 5*time.Second {
		t.Errorf("PollIntervalDuration = %v, хотим 5s", cfg.PollIntervalDuration())
	}
	if cfg.StalenessThresholdDuration() != 20*time.Second {
		t.Errorf("StalenessThresholdDuration = %v, хотим 20s", cfg.StalenessThresholdDuration())
	}
	if len(cfg.RulesRaw) != 0 {
		t.Errorf("RulesRaw должен быть пуст (методология ещё не восстановлена), получили: %v", cfg.RulesRaw)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("ожидали ошибку на несуществующем файле, получили nil")
	}
}

func TestLoad_EmptySymbols(t *testing.T) {
	yamlContent := `
app:
  name: test
  env: development
symbols: []
redis:
  host: localhost
  port: 6379
timeframes:
  - 1m
poll_interval: 5s
staleness_threshold: 20s
`
	path := writeTempConfig(t, yamlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("ожидали ошибку валидации на пустых symbols, получили nil")
	}
}

func TestLoad_EmptyTimeframes(t *testing.T) {
	yamlContent := `
app:
  name: test
  env: development
symbols:
  - BTC_USDT
redis:
  host: localhost
  port: 6379
timeframes: []
poll_interval: 5s
staleness_threshold: 20s
`
	path := writeTempConfig(t, yamlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("ожидали ошибку валидации на пустых timeframes, получили nil")
	}
}

func TestLoad_InvalidPollInterval(t *testing.T) {
	yamlContent := `
app:
  name: test
  env: development
symbols:
  - BTC_USDT
redis:
  host: localhost
  port: 6379
timeframes:
  - 1m
poll_interval: не-длительность
staleness_threshold: 20s
`
	path := writeTempConfig(t, yamlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("ожидали ошибку разбора poll_interval, получили nil")
	}
}

func TestLoad_MissingRedisHost(t *testing.T) {
	yamlContent := `
app:
  name: test
  env: development
symbols:
  - BTC_USDT
redis:
  port: 6379
timeframes:
  - 1m
poll_interval: 5s
staleness_threshold: 20s
`
	path := writeTempConfig(t, yamlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("ожидали ошибку валидации на пустом redis.host, получили nil")
	}
}

func TestLoad_RedisPasswordFromEnv(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "секретный-пароль-теста")

	path := writeTempConfig(t, validConfigYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}

	if cfg.Redis.Password != "секретный-пароль-теста" {
		t.Errorf("Redis.Password = %q, хотим значение из REDIS_PASSWORD", cfg.Redis.Password)
	}
}
