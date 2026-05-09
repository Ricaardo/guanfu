package forecast

import (
	"strings"
	"testing"
)

func TestHorizonCaveatFlagsWeakHistory(t *testing.T) {
	// Gold 180d at 49% / 51 tests → must caveat
	c := HorizonCaveat("gold", 180)
	if c == "" {
		t.Errorf("gold/180d expected caveat, got empty")
	}
	if !strings.Contains(c, "49") {
		t.Errorf("gold/180d caveat should mention 49%% dir hit, got: %s", c)
	}

	// HS300 30d at 47% — also weak
	if HorizonCaveat("hs300", 30) == "" {
		t.Error("hs300/30d expected caveat, got empty")
	}
}

func TestHorizonCaveatStaysSilentWhenReliable(t *testing.T) {
	// QQQ at every recorded horizon is ≥ 0.55
	for _, h := range []int{30, 90, 180} {
		if c := HorizonCaveat("qqq", h); c != "" {
			t.Errorf("qqq/%dd unexpected caveat: %s", h, c)
		}
	}
	// BTC 90d at 65% should be clean
	if c := HorizonCaveat("BTC", 90); c != "" {
		t.Errorf("btc/90d unexpected caveat: %s", c)
	}
}

func TestHorizonCaveatEmptyForUnknownAssetOrHorizon(t *testing.T) {
	// Untested asset/horizon combos return empty — never fabricate warnings.
	if c := HorizonCaveat("nope", 30); c != "" {
		t.Errorf("unknown asset: expected empty, got %s", c)
	}
	if c := HorizonCaveat("gold", 60); c != "" {
		t.Errorf("untested gold/60d: expected empty (no claim), got %s", c)
	}
	if c := HorizonCaveat("", 30); c != "" {
		t.Errorf("empty asset: expected empty, got %s", c)
	}
}

func TestReliabilityForReturnsOk(t *testing.T) {
	r, ok := ReliabilityFor("gold", 180)
	if !ok {
		t.Fatal("gold/180 should be present")
	}
	if r.NTests != 51 {
		t.Errorf("gold/180 NTests=%d, want 51", r.NTests)
	}
	if r.AsOf == "" {
		t.Error("AsOf must not be empty for recorded cells")
	}

	if _, ok := ReliabilityFor("gold", 60); ok {
		t.Error("gold/60 (untested) should return ok=false")
	}
}
