package assetprofile

import "testing"

func TestHorizonsForReturnsFreshSlices(t *testing.T) {
	h := HorizonsFor("qqq")
	if len(h) == 0 {
		t.Fatal("qqq horizons empty")
	}
	h[0] = 999
	if got := HorizonsFor("qqq")[0]; got == 999 {
		t.Fatal("HorizonsFor returned aliased slice")
	}
}

func TestForStockUsesUSStockProfile(t *testing.T) {
	p, ok := For("stock_aapl")
	if !ok {
		t.Fatal("stock profile not found")
	}
	if p.Class != ClassUSStock || p.SkillProfileURI == "" {
		t.Fatalf("unexpected stock profile: %+v", p)
	}
}

func TestReliabilityAndCalibration(t *testing.T) {
	r, ok := ReliabilityFor("gold", 30)
	if !ok || r.DirHit >= 0.50 {
		t.Fatalf("gold/30 reliability = %+v ok=%v, want hard-block row", r, ok)
	}
	if got := ConformalScale("qqq", 90); got != 1.80 {
		t.Fatalf("qqq/90 conformal scale = %.2f, want 1.80", got)
	}
	if got := ConformalScale("btc", 90); got != 1 {
		t.Fatalf("btc/90 conformal scale = %.2f, want 1", got)
	}
}

func TestHorizonWeightMultiplier(t *testing.T) {
	if got := HorizonWeightMultiplier("qqq", "return_30d", 30); got != 1.25 {
		t.Fatalf("return_30d/30 multiplier = %.2f, want 1.25", got)
	}
	if got := HorizonWeightMultiplier("gold", "real_yield_10y", 180); got != 1.25 {
		t.Fatalf("real_yield_10y/180 multiplier = %.2f, want 1.25", got)
	}
	if got := HorizonWeightMultiplier("stock_aapl", "unknown", 90); got != 1 {
		t.Fatalf("unknown feature multiplier = %.2f, want 1", got)
	}
}

func TestExpectedFeaturesFor(t *testing.T) {
	eq := ExpectedFeaturesFor("qqq")
	if !contains(eq, "put_call_252d_percentile") || !contains(eq, "cape") {
		t.Fatalf("qqq expected features missing equity fields: %v", eq)
	}
	stock := ExpectedFeaturesFor("stock_aapl")
	if contains(stock, "cape") || !contains(stock, "yield_curve") {
		t.Fatalf("stock expected features should use generic macro without CAPE: %v", stock)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
