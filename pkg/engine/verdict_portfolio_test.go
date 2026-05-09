package engine

import (
	"strings"
	"testing"

	"github.com/Ricaardo/guanfu/pkg/portfolio"
)

func sampleVerdict(stance string) *Verdict {
	return &Verdict{Date: "2026-05-09", Stance: stance}
}

func TestAnnotateNoOpWhenPortfolioNil(t *testing.T) {
	v := sampleVerdict("偏积累")
	AnnotateVerdictWithPortfolio(v, "btc", nil, 80000, nil)
	if v.PortfolioContext != nil {
		t.Error("nil portfolio should not annotate")
	}
}

func TestAnnotateNoOpWhenVerdictNil(t *testing.T) {
	p := &portfolio.Portfolio{SchemaVersion: 1}
	AnnotateVerdictWithPortfolio(nil, "btc", p, 80000, nil)
	// Should not panic.
}

func TestAnnotateFlagsOverweightWithStrongCaution(t *testing.T) {
	p := &portfolio.Portfolio{
		SchemaVersion: 1,
		Holdings: map[string]portfolio.Holding{
			"btc":  {Amount: 0.5},
			"cash": {USD: 10000},
		},
		Preferences: portfolio.Preferences{
			HorizonYears: 5,
			RiskBudget:   "moderate",
			CeilingPct:   map[string]float64{"btc": 25},
		},
	}
	v := sampleVerdict("偏积累")
	prices := map[string]float64{"btc": 80000}
	// BTC value = 0.5 * 80000 = 40000; cash = 10000; total = 50000; btc = 80%
	AnnotateVerdictWithPortfolio(v, "btc", p, 80000, prices)
	if v.PortfolioContext == nil {
		t.Fatal("PortfolioContext should be set")
	}
	ctx := v.PortfolioContext
	if !ctx.Overweight {
		t.Errorf("weight %.1f%% vs ceiling %.1f%% → expected overweight",
			ctx.CurrentWeightPct, ctx.CeilingPct)
	}
	if ctx.CurrentWeightPct < 75 || ctx.CurrentWeightPct > 85 {
		t.Errorf("BTC weight expected ~80%%, got %.1f", ctx.CurrentWeightPct)
	}
	if len(ctx.Notes) == 0 {
		t.Error("overweight should produce at least one note")
	}
	if !strings.Contains(strings.Join(ctx.Notes, " "), "不追加") {
		t.Errorf("note should warn against adding: %v", ctx.Notes)
	}
}

func TestAnnotateReportsRoomWhenUnderCeiling(t *testing.T) {
	p := &portfolio.Portfolio{
		SchemaVersion: 1,
		Holdings: map[string]portfolio.Holding{
			"btc":  {Amount: 0.1},   // 8000 at 80k
			"cash": {USD: 92000},    // total 100k, BTC weight 8%
		},
		Preferences: portfolio.Preferences{
			HorizonYears: 5,
			CeilingPct:   map[string]float64{"btc": 25},
		},
	}
	v := sampleVerdict("偏积累")
	AnnotateVerdictWithPortfolio(v, "btc", p, 80000, map[string]float64{"btc": 80000})
	ctx := v.PortfolioContext
	if ctx == nil || ctx.Overweight {
		t.Errorf("under-ceiling should not be overweight: %#v", ctx)
	}
	if ctx.RoomToCeilingPct <= 0 {
		t.Errorf("expected positive room to ceiling, got %.1f", ctx.RoomToCeilingPct)
	}
}

func TestAnnotateShortHorizonMismatch(t *testing.T) {
	p := &portfolio.Portfolio{
		SchemaVersion: 1,
		Holdings: map[string]portfolio.Holding{
			"btc": {Amount: 0.1},
		},
		Preferences: portfolio.Preferences{
			HorizonYears: 0, // unset → HorizonMatch="unknown"
		},
	}
	v := sampleVerdict("偏积累")
	AnnotateVerdictWithPortfolio(v, "btc", p, 80000, nil)
	if v.PortfolioContext.HorizonMatch != "unknown" {
		t.Errorf("horizon=0 should be unknown, got %q", v.PortfolioContext.HorizonMatch)
	}
}

func TestAnnotateAggressiveVsDefensiveStance(t *testing.T) {
	p := &portfolio.Portfolio{
		SchemaVersion: 1,
		Preferences: portfolio.Preferences{
			HorizonYears: 5,
			RiskBudget:   "aggressive",
		},
	}
	v := sampleVerdict("高防守倾向")
	AnnotateVerdictWithPortfolio(v, "btc", p, 0, nil)
	notes := strings.Join(v.PortfolioContext.Notes, " ")
	if !strings.Contains(notes, "aggressive") {
		t.Errorf("expected aggressive-vs-defensive hint, got: %v", v.PortfolioContext.Notes)
	}
}

func TestAnnotateConservativeVsAccumulate(t *testing.T) {
	p := &portfolio.Portfolio{
		SchemaVersion: 1,
		Preferences: portfolio.Preferences{
			HorizonYears: 5,
			RiskBudget:   "conservative",
		},
	}
	v := sampleVerdict("强积累倾向")
	AnnotateVerdictWithPortfolio(v, "btc", p, 0, nil)
	notes := strings.Join(v.PortfolioContext.Notes, " ")
	if !strings.Contains(notes, "conservative") {
		t.Errorf("expected conservative hint vs accumulation stance, got: %v", v.PortfolioContext.Notes)
	}
}
