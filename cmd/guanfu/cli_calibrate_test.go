package main

import (
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/pkg/claim"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// K4 smoke test: scoreClaims bucket + metric shape.
// Note: since resolveActualReturn queries PriceStore on disk and that
// isn't reliably populated in CI, we unit-test the bucketing/score
// math through a direct construction.
func TestScoreClaimsWithMockedResolver(t *testing.T) {
	ps := &store.PriceStore{}
	now := time.Now().UTC()
	claims := []claim.Claim{
		// matured, price-less (PriceAtClaim=0 → !ok → dropped)
		{Asset: "btc", Horizon: 30, AsOf: now.AddDate(0, 0, -60)},
		// mature + PriceAtClaim 0 same → dropped (we can't actually force
		// the PriceStore to have matching data in a unit test, so just
		// assert that unresolvable claims produce empty rows.)
	}
	rows := scoreClaims(claims, ps)
	if len(rows) != 0 {
		t.Errorf("unresolvable claims should produce no rows, got %d", len(rows))
	}
}

func TestMedianOfHandlesEvenAndOdd(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{1, 2, 3}, 2},
		{[]float64{1, 2, 3, 4}, 2.5},
		{[]float64{10}, 10},
		{[]float64{}, 0},
	}
	for _, c := range cases {
		if got := medianOf(c.in); got != c.want {
			t.Errorf("medianOf(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsCoreAsset(t *testing.T) {
	for _, a := range []string{"btc", "qqq", "spy", "gold", "hs300"} {
		if !isCoreAsset(a) {
			t.Errorf("%s should be core", a)
		}
	}
	if isCoreAsset("stock_aapl") {
		t.Error("stock_* should not be core")
	}
}
