package rules

import "testing"

// TestEvaluate_AlwaysHold_UntilMethodologyRestored фиксирует текущее,
// намеренное поведение заглушки: НЕЗАВИСИМО от входных T/V/P, Evaluate
// обязана вернуть HOLD/rules_not_configured, пока методология
// TVP_SNIPER не восстановлена (см. TODO в шапке rules.go). Этот тест
// должен начать падать в тот момент, когда кто-то реализует реальную
// логику в Evaluate — это ожидаемо и правильно: тест тогда нужно
// переписать вместе с реализацией, а не подгонять реализацию под тест.
func TestEvaluate_AlwaysHold_UntilMethodologyRestored(t *testing.T) {
	cases := []struct {
		name  string
		input Input
	}{
		{
			name:  "пустой вход",
			input: Input{},
		},
		{
			name: "заведомо 'бычий' вход (T up + V spike + P imbalance>1.5)",
			input: Input{
				Trend: map[string]TrendInput{
					"1m":  {Direction: "up"},
					"8m":  {Direction: "up"},
					"24m": {Direction: "up", RSI: 70},
				},
				Volume: map[string]VolumeInput{
					"1m": {BuyVol: 100000, SellVol: 100, Spike: true},
				},
				Pressure: PressureInput{BidVol: 90000, AskVol: 10000, Imbalance: 9.0},
			},
		},
		{
			name: "заведомо 'медвежий' вход (T down + V spike + P imbalance<1)",
			input: Input{
				Trend: map[string]TrendInput{
					"1m":  {Direction: "down"},
					"8m":  {Direction: "down"},
					"24m": {Direction: "down", RSI: 30},
				},
				Volume: map[string]VolumeInput{
					"1m": {BuyVol: 100, SellVol: 100000, Spike: true},
				},
				Pressure: PressureInput{BidVol: 10000, AskVol: 90000, Imbalance: 0.11},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.input)
			if got.Type != SignalHold {
				t.Errorf("Type = %q, хотим %q (заглушка не должна решать LONG/SHORT)", got.Type, SignalHold)
			}
			if got.Reason != ReasonRulesNotConfigured {
				t.Errorf("Reason = %q, хотим %q", got.Reason, ReasonRulesNotConfigured)
			}
		})
	}
}
