package gateway

import (
	"encoding/json"
	"errors"
	"testing"
)

// --- parseSymbolFromCandleName ---
//
// Регрессионные тесты на находку 1.3 независимого аудита (агент
// OpenCode/Sonnet-5, 2026-08-10): раньше был захардкоженный name[3:],
// который предполагал ровно 3 символа префикса ("1m_") и молча ломался
// бы для более длинных префиксов таймфрейма ("15m_", "30m_" — 4 символа).

func TestParseSymbolFromCandleName_StandardOneMinutePrefix(t *testing.T) {
	got := parseSymbolFromCandleName("1m_BTC_USDT")
	want := "BTC_USDT"
	if got != want {
		t.Errorf("parseSymbolFromCandleName(%q) = %q, want %q", "1m_BTC_USDT", got, want)
	}
}

func TestParseSymbolFromCandleName_LongerTimeframePrefix(t *testing.T) {
	// Гипотетический случай — bot сейчас подписывается только на 1m
	// (см. SubscribeCandlesticks), но если в будущем добавится 15m/30m,
	// разбор по разделителю "_" должен сработать корректно, в отличие
	// от старого захардкоженного name[3:].
	cases := map[string]string{
		"15m_ETH_USDT": "ETH_USDT",
		"30m_SOL_USDT": "SOL_USDT",
		"1h_BTC_USDT":  "BTC_USDT",
	}
	for input, want := range cases {
		got := parseSymbolFromCandleName(input)
		if got != want {
			t.Errorf("parseSymbolFromCandleName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseSymbolFromCandleName_NoSeparatorFallsBackToInput(t *testing.T) {
	// Неожиданный формат без "_" вообще — лучше вернуть name как есть
	// (данные не теряются, хоть и публикуются под странным символом),
	// чем паниковать или возвращать пустую строку.
	got := parseSymbolFromCandleName("unexpectedformat")
	if got != "unexpectedformat" {
		t.Errorf("parseSymbolFromCandleName без разделителя = %q, want исходную строку без изменений", got)
	}
}

// --- parseLiquidations ---

func TestParseLiquidations_ArrayFormat(t *testing.T) {
	raw := json.RawMessage(`[{"contract":"BTC_USDT","size":"1.5","price":"50000","time_ms":1000}]`)
	liqs, err := parseLiquidations(raw)
	if err != nil {
		t.Fatalf("parseLiquidations(array) вернул ошибку: %v", err)
	}
	if len(liqs) != 1 || liqs[0].Contract != "BTC_USDT" {
		t.Errorf("parseLiquidations(array) = %+v, want 1 элемент с Contract=BTC_USDT", liqs)
	}
}

func TestParseLiquidations_SingleObjectFormat(t *testing.T) {
	raw := json.RawMessage(`{"contract":"ETH_USDT","size":"2.0","price":"3000","time_ms":2000}`)
	liqs, err := parseLiquidations(raw)
	if err != nil {
		t.Fatalf("parseLiquidations(object) вернул ошибку: %v", err)
	}
	if len(liqs) != 1 || liqs[0].Contract != "ETH_USDT" {
		t.Errorf("parseLiquidations(object) = %+v, want 1 элемент с Contract=ETH_USDT", liqs)
	}
}

// TestParseLiquidations_InvalidJSONWrapsUnderlyingError — регрессионный
// тест на находку 5.2 первого раунда независимого аудита parser.go
// (агент OpenCode/Sonnet-5, 2026-08-10): раньше ошибка не оборачивала
// исходную причину сбоя парсинга (%w отсутствовал), что затрудняло
// диагностику протокольных изменений Gate.io. Теперь errors.Unwrap
// должен возвращать исходную ошибку json.Unmarshal, не nil.
func TestParseLiquidations_InvalidJSONWrapsUnderlyingError(t *testing.T) {
	raw := json.RawMessage(`not valid json at all`)
	_, err := parseLiquidations(raw)
	if err == nil {
		t.Fatal("parseLiquidations(невалидный JSON) должен вернуть ошибку")
	}
	if errors.Unwrap(err) == nil {
		t.Error("ошибка должна оборачивать исходную причину через %w (errors.Unwrap вернул nil)")
	}
}
