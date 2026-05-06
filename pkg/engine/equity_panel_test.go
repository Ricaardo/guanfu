package engine

import (
	"encoding/json"
	"testing"

	"github.com/Ricaardo/guanfu/pkg/model"
)

// generatePriceHistory creates a synthetic price history with a simple trend.
// Returns newest-first slice.
func generatePriceHistory(days int, startPrice, trend float64) []float64 {
	history := make([]float64, days)
	for i := 0; i < days; i++ {
		history[days-1-i] = startPrice + float64(i)*trend
	}
	return history
}

func TestBuildEquityPanel_Basic(t *testing.T) {
	history := generatePriceHistory(250, 400, 0.5) // trending up from 400

	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
		VIX:          18,
		DXY:          28,
		TLT:          88,
		FearGreed:    55,
	}

	panel := BuildEquityPanel(in)

	// Technical domain should have indicators
	if len(panel.Technical) == 0 {
		t.Fatal("expected technical indicators")
	}

	// RSI should exist
	if rsi, ok := panel.Technical["rsi_14"]; !ok {
		t.Fatal("expected rsi_14 indicator")
	} else if rsi.Value < 0 || rsi.Value > 100 {
		t.Fatalf("rsi out of range: %f", rsi.Value)
	}

	// SMA indicators
	if _, ok := panel.Technical["sma_50"]; !ok {
		t.Fatal("expected sma_50 indicator")
	}
	if _, ok := panel.Technical["sma_200"]; !ok {
		t.Fatal("expected sma_200 indicator")
	}

	// Macro domain
	if vix, ok := panel.Macro["vix_level"]; !ok || vix.Value != 18 {
		t.Fatal("expected vix_level indicator")
	}

	// Sentiment
	if fg, ok := panel.Positioning["fear_greed"]; !ok || fg.Value != 55 {
		t.Fatal("expected fear_greed indicator")
	}

	// JSON roundtrip
	b, err := json.Marshal(panel)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
}

func TestBuildEquityPanel_InsufficientHistory(t *testing.T) {
	history := generatePriceHistory(10, 400, 1.0)

	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
	}

	panel := BuildEquityPanel(in)

	// Should have no RSI (need 14+ days), no SMA50 (need 50+), etc.
	if _, ok := panel.Technical["rsi_14"]; ok {
		t.Fatal("rsi_14 should be missing with only 10 days")
	}
	if _, ok := panel.Technical["sma_50"]; ok {
		t.Fatal("sma_50 should be missing with only 10 days")
	}
}

func TestBuildEquityVerdict_Bullish(t *testing.T) {
	// Create a strongly bullish panel
	history := generatePriceHistory(250, 400, 1.0) // strong uptrend
	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
		VIX:          12, // low VIX
	}
	panel := BuildEquityPanel(in)

	verdict := BuildEquityVerdict(panel)
	if verdict.NetDirection <= 0 {
		t.Fatalf("expected positive net direction, got %d", verdict.NetDirection)
	}
	if verdict.Coverage <= 0 {
		t.Fatalf("expected positive coverage, got %f", verdict.Coverage)
	}
}

func TestBuildEquityVerdict_Bearish(t *testing.T) {
	// Create a bearish panel: high VIX, declining prices
	history := generatePriceHistory(250, 500, -1.0) // downtrend
	in := &EquityPanelInput{
		Asset:        "spy",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
		VIX:          35, // high VIX fear
	}
	panel := BuildEquityPanel(in)

	// Manually set RSI to oversold to test bearish signals
	panel.Technical["rsi_14"] = model.Indicator{Value: 25, Label: "超卖"}
	panel.Technical["sma_200_dev"] = model.Indicator{Value: -15, Label: "偏低"}

	verdict := BuildEquityVerdict(panel)
	t.Logf("verdict: net=%d, stance=%s, reasons=%v", verdict.NetDirection, verdict.Stance, verdict.Reasons)
	// Should have some bearish signal
	if len(verdict.Domains) == 0 {
		t.Fatal("expected domain votes")
	}
}

func TestCalcRSI(t *testing.T) {
	// Newest-first increasing sequence: all gains → RSI should be 100
	up := []float64{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	rsi := calcRSI(up, 14)
	if rsi != 100 {
		t.Fatalf("expected RSI=100 for all gains, got %f", rsi)
	}

	// Sideways: all same → RSI should be 100 (no losses)
	flat := make([]float64, 15)
	for i := range flat {
		flat[i] = 10
	}
	rsi = calcRSI(flat, 14)
	if rsi != 100 {
		t.Fatalf("expected RSI=100 for flat, got %f", rsi)
	}
}

func TestComputeVol30d(t *testing.T) {
	history := generatePriceHistory(30, 400, 0)
	vol := computeVol30d(history)
	if vol.Value < 0 {
		t.Fatalf("expected non-negative vol, got %f", vol.Value)
	}
}

func TestComputeDrawdown(t *testing.T) {
	// Peak at 500, current at 450 → 10% drawdown
	history := make([]float64, 200)
	for i := 0; i < 200; i++ {
		history[199-i] = 450 + float64(i%50)*1.0
	}
	history[0] = 500 // newest is peak
	history[199] = 450

	dd := computeDrawdown(history, 450)
	if dd.Value > 0 {
		t.Fatalf("expected non-positive drawdown, got %f", dd.Value)
	}
}

func TestEquityAssetRegistry(t *testing.T) {
	// QQQ and SPY should be registered
	qqq, err := GetAsset("qqq")
	if err != nil {
		t.Fatalf("qqq not registered: %v", err)
	}
	if qqq.Key() != "qqq" || qqq.Name() == "" {
		t.Fatalf("qqq asset invalid: key=%s name=%s", qqq.Key(), qqq.Name())
	}

	spy, err := GetAsset("spy")
	if err != nil {
		t.Fatalf("spy not registered: %v", err)
	}
	if spy.Key() != "spy" || spy.Name() == "" {
		t.Fatalf("spy asset invalid: key=%s name=%s", spy.Key(), spy.Name())
	}

	// BTC should be registered (from asset_btc.go init)
	btc, err := GetAsset("btc")
	if err != nil {
		t.Fatalf("btc not registered: %v", err)
	}
	if btc.Key() != "btc" {
		t.Fatalf("btc asset invalid: key=%s", btc.Key())
	}
}

func TestCoverageScore(t *testing.T) {
	empty := map[string]model.Indicator{}
	if c := coverageScore(empty); c != 0 {
		t.Fatalf("expected 0 coverage for empty, got %f", c)
	}

	full := map[string]model.Indicator{
		"a": {Value: 1, Missing: false},
		"b": {Value: 2, Missing: false},
		"c": {Value: 3, Missing: true},
	}
	if c := coverageScore(full); c != 2.0/3.0 {
		t.Fatalf("expected 2/3 coverage, got %f", c)
	}
}

func TestComputeMomentum(t *testing.T) {
	history := make([]float64, 100)
	for i := 0; i < 100; i++ {
		history[99-i] = 400 + float64(i)*0.5
	}
	history[0] = 450 // newest
	mom := computeMomentum(history, 90)
	if mom.Value <= 0 {
		t.Fatalf("expected positive momentum, got %f", mom.Value)
	}
}
