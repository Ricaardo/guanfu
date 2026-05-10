package client

import (
	"testing"
)

func TestDefillamaStablecoinSourceInterface(t *testing.T) {
	var s Source = DefillamaStablecoinSource{}
	if s.Key() == "" {
		t.Error("Key must not be empty")
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName must not be empty")
	}
}

func TestParseDefillamaDate(t *testing.T) {
	// unix seconds (float, as JSON parses)
	if tm, ok := parseDefillamaDate(float64(1700000000)); !ok || tm.Year() != 2023 {
		t.Errorf("float unix failed: %v %v", tm, ok)
	}
	// ISO date
	if tm, ok := parseDefillamaDate("2025-03-15"); !ok || tm.Year() != 2025 || tm.Month() != 3 {
		t.Errorf("iso date failed: %v %v", tm, ok)
	}
	// unix seconds as string (current DefiLlama schema)
	if tm, ok := parseDefillamaDate("1700000000"); !ok || tm.Year() != 2023 {
		t.Errorf("string unix failed: %v %v", tm, ok)
	}
	// garbage
	if _, ok := parseDefillamaDate([]string{"nope"}); ok {
		t.Error("garbage should not parse")
	}
}

func TestSumPeggedValues(t *testing.T) {
	m := map[string]any{
		"peggedUSD": float64(150e9),
		"peggedEUR": float64(3e9),
		"bogus":     "string not number",
	}
	got := sumPeggedValues(m)
	if got != 153e9 {
		t.Errorf("sum = %v, want 153e9", got)
	}
}
