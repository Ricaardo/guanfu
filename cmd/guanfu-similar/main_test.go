package main

import (
	"math"
	"testing"

	"github.com/Ricaardo/guanfu/internal/model"
)

func TestComparePanelsUsesSharedQuantiles(t *testing.T) {
	current := &model.IndicatorPanel{
		Cycle: map[string]model.Indicator{
			"ahr999": {Quantile: 0.20},
		},
		Valuation: map[string]model.Indicator{
			"mayer": {Quantile: 0.40},
		},
	}
	history := &model.IndicatorPanel{
		Cycle: map[string]model.Indicator{
			"ahr999": {Quantile: 0.30},
		},
		Valuation: map[string]model.Indicator{
			"mayer":  {Quantile: 0.60},
			"unused": {Quantile: 0.99},
		},
	}

	got := comparePanels(current, history)
	wantDistance := math.Sqrt((0.10*0.10 + 0.20*0.20) / 2)
	if got.Matched != 2 {
		t.Fatalf("matched = %d, want 2", got.Matched)
	}
	if math.Abs(got.Distance-wantDistance) > 1e-12 {
		t.Fatalf("distance = %.12f, want %.12f", got.Distance, wantDistance)
	}
}

func TestComparePanelsRequiresSharedQuantiles(t *testing.T) {
	got := comparePanels(&model.IndicatorPanel{}, &model.IndicatorPanel{})
	if got.Matched != 0 || !math.IsInf(got.Distance, 1) {
		t.Fatalf("got %+v, want no match with +Inf distance", got)
	}
}
