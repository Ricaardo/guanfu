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
	if p.Class != ClassUSStock || p.SkillProfileURI == "" || len(p.ReadingDomains) == 0 {
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

func TestReadingDomainsFor(t *testing.T) {
	gold := ReadingDomainsFor("gold")
	if !containsDomain(gold, "macro") || containsDomain(gold, "network") {
		t.Fatalf("gold reading domains should be gold-specific: %+v", gold)
	}
	gold[0].Title = "mutated"
	if ReadingDomainsFor("gold")[0].Title == "mutated" {
		t.Fatal("ReadingDomainsFor returned aliased slice")
	}
}

func TestVerdictPolicyFor(t *testing.T) {
	eq, ok := VerdictPolicyFor("qqq")
	if !ok {
		t.Fatal("qqq verdict policy missing")
	}
	if eq.Key != "equity_index" || eq.BullThreshold != 3 || !contains(eq.DomainOrder, "positioning") {
		t.Fatalf("unexpected qqq verdict policy: %+v", eq)
	}
	stock, ok := VerdictPolicyFor("stock_aapl")
	if !ok {
		t.Fatal("stock verdict policy missing")
	}
	if stock.Key != "us_stock" || stock.BullStance == "" {
		t.Fatalf("unexpected stock verdict policy: %+v", stock)
	}
	stock.DomainOrder[0] = "mutated"
	if got, _ := VerdictPolicyFor("stock_aapl"); got.DomainOrder[0] == "mutated" {
		t.Fatal("VerdictPolicyFor returned aliased domain order")
	}
	for _, asset := range []string{"btc", "qqq", "spy", "gold", "stock_aapl"} {
		policy, ok := VerdictPolicyFor(asset)
		if !ok {
			t.Fatalf("%s verdict policy missing", asset)
		}
		if len(policy.DomainOrder) == 0 || policy.BullRegime == "" || policy.NeutralRegime == "" || policy.BearRegime == "" {
			t.Fatalf("%s verdict policy has incomplete regime metadata: %+v", asset, policy)
		}
		if policy.BullStance == "" || policy.NeutralStance == "" || policy.BearStance == "" {
			t.Fatalf("%s verdict policy has incomplete stance metadata: %+v", asset, policy)
		}
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

func containsDomain(values []DomainSpec, want string) bool {
	for _, v := range values {
		if v.Key == want {
			return true
		}
	}
	return false
}
