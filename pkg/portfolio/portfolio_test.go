package portfolio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const samplePortfolio = `{
  "schema_version": 1,
  "holdings": {
    "BTC":  {"amount": 0.5, "cost_basis_usd": 30000, "acquired": "2023-06"},
    "qqq":  {"shares": 100, "cost_basis_usd": 400},
    "cash": {"usd": 20000, "cny": 70000}
  },
  "preferences": {
    "horizon_years": 5,
    "risk_budget": "moderate",
    "home_currency": "CNY",
    "ceiling_pct": {"BTC": 25, "equity": 60}
  },
  "behavior": {
    "cooldown_hours": 4,
    "fomo_threshold_pct": 20
  }
}`

func writeSample(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "portfolio.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileIsNotError(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "doesnotexist.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if p != nil {
		t.Errorf("missing file should return nil portfolio, got %#v", p)
	}
}

func TestLoadValidPortfolio(t *testing.T) {
	path := writeSample(t, samplePortfolio)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", p.SchemaVersion)
	}
	if p.Preferences.HorizonYears != 5 {
		t.Errorf("horizon_years = %d, want 5", p.Preferences.HorizonYears)
	}
	if p.Preferences.RiskBudget != "moderate" {
		t.Errorf("risk_budget = %q, want moderate", p.Preferences.RiskBudget)
	}
}

func TestLoadNormalizesAssetKeys(t *testing.T) {
	// "BTC" upper in source → "btc" after Validate
	path := writeSample(t, samplePortfolio)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Holdings["btc"]; !ok {
		t.Error("BTC key should be normalized to btc")
	}
	if p.Preferences.CeilingPct["btc"] != 25 {
		t.Errorf("ceiling BTC: expected 25, got %v", p.Preferences.CeilingPct)
	}
}

func TestValidateRejectsFutureSchemaVersion(t *testing.T) {
	path := writeSample(t, `{"schema_version": 99}`)
	if _, err := Load(path); err == nil {
		t.Error("future schema_version should error")
	}
}

func TestValidateRejectsBadRiskBudget(t *testing.T) {
	path := writeSample(t, `{"schema_version": 1, "preferences": {"risk_budget": "ultra"}}`)
	if _, err := Load(path); err == nil {
		t.Error("unknown risk_budget should error")
	}
}

func TestHoldingForIsCaseInsensitive(t *testing.T) {
	path := writeSample(t, samplePortfolio)
	p, _ := Load(path)
	if _, ok := p.HoldingFor("BTC"); !ok {
		t.Error("HoldingFor(BTC) should find btc holding")
	}
	if _, ok := p.HoldingFor("btc"); !ok {
		t.Error("HoldingFor(btc) should find btc holding")
	}
}

func TestPositionValueUSD(t *testing.T) {
	path := writeSample(t, samplePortfolio)
	p, _ := Load(path)

	// BTC at $80k → 0.5 * 80000 = 40000
	if got := p.PositionValueUSD("btc", 80000); got != 40000 {
		t.Errorf("BTC @ 80k = %v, want 40000", got)
	}
	// QQQ at $500 → 100 * 500 = 50000
	if got := p.PositionValueUSD("qqq", 500); got != 50000 {
		t.Errorf("QQQ @ 500 = %v, want 50000", got)
	}
	// Cash: 20000 + 70000/7 = 30000
	if got := p.PositionValueUSD("cash", 0); got != 30000 {
		t.Errorf("cash = %v, want 30000", got)
	}
	// Unknown asset → 0
	if got := p.PositionValueUSD("foo", 100); got != 0 {
		t.Errorf("unknown asset should be 0, got %v", got)
	}
}

func TestWeightOfAndCeiling(t *testing.T) {
	path := writeSample(t, samplePortfolio)
	p, _ := Load(path)
	prices := map[string]float64{"btc": 80000, "qqq": 500}
	// Total = 40000 + 50000 + 30000 = 120000
	// BTC weight = 40000/120000 ≈ 33.3%
	w := p.WeightOf("btc", prices) * 100
	if w < 33 || w > 34 {
		t.Errorf("BTC weight ≈ 33.3%%, got %.2f", w)
	}
	// ceiling 25 → user is overweight BTC
	ceiling := p.CeilingFor("btc")
	if ceiling != 25 {
		t.Errorf("ceiling = %v, want 25", ceiling)
	}
	if w <= ceiling {
		t.Errorf("user should be overweight BTC: w=%.2f%% ceil=%.2f%%", w, ceiling)
	}
}

func TestNilPortfolioSafeToUse(t *testing.T) {
	var p *Portfolio
	if _, ok := p.HoldingFor("btc"); ok {
		t.Error("nil portfolio should not find anything")
	}
	if got := p.CeilingFor("btc"); got != 0 {
		t.Error("nil portfolio ceiling should be 0")
	}
	if got := p.PositionValueUSD("btc", 100); got != 0 {
		// PositionValueUSD panics on nil — but call through HoldingFor is safe.
		// Confirm the current contract is graceful.
		t.Errorf("nil portfolio PositionValueUSD should be 0, got %v", got)
	}
}

// Smoke test: sample JSON round-trips through json.Marshal unchanged.
func TestMarshalRoundtrip(t *testing.T) {
	p := &Portfolio{
		SchemaVersion: 1,
		Holdings: map[string]Holding{
			"btc": {Amount: 0.1, CostBasisUSD: 50000},
		},
		Preferences: Preferences{HorizonYears: 3, RiskBudget: "conservative"},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Portfolio
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Holdings["btc"].Amount != 0.1 {
		t.Error("round-trip lost holding")
	}
}

// L7: multi-currency conversion.
func TestCurrencyPairUSDRateFallbacks(t *testing.T) {
	cases := []struct {
		in       string
		cnyRate  float64
		want     float64
		wantZero bool
	}{
		{"USD", 0, 1.0, false},
		{"", 0, 1.0, false},
		{"CNY", 7.15, 7.15, false},
		{"CNY", 0, 7.2, false}, // fallback
		{"JPY", 0, 150.0, false},
		{"EUR", 0, 0.92, false},
		{"nonsense", 0, 0, true},
	}
	for _, c := range cases {
		got := CurrencyPairUSDRate(c.in, c.cnyRate)
		if c.wantZero {
			if got != 0 {
				t.Errorf("%q should be 0, got %v", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%q @ cny=%v = %v, want %v", c.in, c.cnyRate, got, c.want)
		}
	}
}

func TestPortfolioConvertUSD(t *testing.T) {
	pCNY := &Portfolio{Preferences: Preferences{HomeCurrency: "CNY"}}
	amt, cur := pCNY.ConvertUSD(1000, 7.15)
	if cur != "CNY" || amt != 7150 {
		t.Errorf("CNY convert: got %v %s, want 7150 CNY", amt, cur)
	}

	pUSD := &Portfolio{Preferences: Preferences{HomeCurrency: "USD"}}
	amt, cur = pUSD.ConvertUSD(1000, 7.15)
	if cur != "USD" || amt != 1000 {
		t.Errorf("USD passthrough failed: %v %s", amt, cur)
	}

	// nil portfolio → no conversion
	var pNil *Portfolio
	amt, cur = pNil.ConvertUSD(1000, 7.15)
	if cur != "USD" || amt != 1000 {
		t.Errorf("nil portfolio should pass through")
	}
}
