package forecast

import (
	"strings"
	"testing"
)

func TestHorizonCaveatFlagsWeakHistory(t *testing.T) {
	// Gold 180d at 49% / 51 tests → hard-block caveat (dir_hit < 0.50)
	c := HorizonCaveat("gold", 180)
	if c == "" {
		t.Errorf("gold/180d expected caveat, got empty")
	}
	if !strings.Contains(c, "低于随机") {
		t.Errorf("gold/180d (49%%) should hit hard-block caveat, got: %s", c)
	}

	// Gold 30d at 51% → within [0.50, 0.55): "approaching random" caveat
	c30 := HorizonCaveat("gold", 30)
	if c30 == "" {
		t.Errorf("gold/30d expected caveat, got empty")
	}
	if !strings.Contains(c30, "接近随机") {
		t.Errorf("gold/30d (51%%) should hit approaching-random caveat, got: %s", c30)
	}

}

func TestIsHardBlocked(t *testing.T) {
	// Gold 180d at 0.49 → blocked; 30d at 0.51 → not blocked
	if !IsHardBlocked("gold", 180) {
		t.Error("gold/180 at 49%% should be hard-blocked")
	}
	if IsHardBlocked("gold", 30) {
		t.Error("gold/30 at 51%% should NOT be hard-blocked (only above-random caveat)")
	}
	// QQQ reliable → never blocked
	for _, h := range []int{30, 90, 180} {
		if IsHardBlocked("qqq", h) {
			t.Errorf("qqq/%dd should not be hard-blocked", h)
		}
	}
	// Unknown cell → not blocked (no evidence)
	if IsHardBlocked("nope", 30) {
		t.Error("unknown asset should not be hard-blocked")
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
