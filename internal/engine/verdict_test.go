package engine

import (
	"math"
	"testing"

	"github.com/Ricaardo/guanfu/internal/model"
)

// build a panel with a small set of indicators for testing.
func newTestPanel() *model.IndicatorPanel {
	return &model.IndicatorPanel{
		Date:        "2026-05-03",
		Cycle:       map[string]model.Indicator{},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	}
}

func TestVerdictMissingIsolation(t *testing.T) {
	// Missing 指标必须不影响其他指标的计票。
	p := newTestPanel()
	// 1 个真实看涨指标 + 1 个 missing 看跌指标
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7} // bull
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Missing: true, Value: 1.5} // would be bear if not missing

	v := BuildVerdict(p)

	cycle := v.Domains[0]
	if cycle.Vote != +1 {
		t.Fatalf("expected cycle vote +1 (only mayer counted), got %d", cycle.Vote)
	}
	if len(cycle.Skipped) != 1 || cycle.Skipped[0] != "pi_cycle_top_ratio" {
		t.Fatalf("expected pi_cycle skipped, got %v", cycle.Skipped)
	}
	if len(cycle.Bearish) != 0 {
		t.Fatalf("missing pi_cycle leaked into bearish votes: %v", cycle.Bearish)
	}
}

func TestVerdictNaNIsAvailable(t *testing.T) {
	p := newTestPanel()
	p.Cycle["mayer_multiple"] = model.Indicator{Value: math.NaN()}
	v := BuildVerdict(p)
	cycle := v.Domains[0]
	if len(cycle.Skipped) != 1 {
		t.Fatalf("NaN should be skipped, got skipped=%v", cycle.Skipped)
	}
}

func TestVerdictBullStanceOnStrongConsensus(t *testing.T) {
	p := newTestPanel()
	// 5 个域看涨
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7}              // bull
	p.Valuation["ahr999"] = model.Indicator{Value: 0.4}                   // bull
	p.Network["hash_ribbons"] = model.Indicator{Label: "上行（扩张）"}    // bull
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: -0.005}    // bull
	p.Positioning["oi_to_mc"] = model.Indicator{Value: 0.012}             // bull
	p.Macro["m2_yoy"] = model.Indicator{Value: 6.0}                       // bull
	p.Flow["etf_net_flow_30d_usd"] = model.Indicator{Value: 2e9}          // bull
	p.Technical["rsi_14"] = model.Indicator{Value: 28}                    // bull
	p.CrossAsset["btc_spy_corr_30d"] = model.Indicator{Value: 0.2}        // bull

	v := BuildVerdict(p)
	if v.NetDirection < 5 {
		t.Fatalf("expected net direction >= 5, got %d", v.NetDirection)
	}
	if v.Stance != "强积累倾向" {
		t.Fatalf("expected 强积累倾向, got %q", v.Stance)
	}
	if v.Regime != "牛市" {
		t.Fatalf("expected 牛市, got %q", v.Regime)
	}
}

func TestVerdictTopProximity(t *testing.T) {
	p := newTestPanel()
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Value: 1.0} // hard top signal
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 2.6}     // top
	p.Valuation["mvrv_z_score"] = model.Indicator{Value: 7.5}   // top
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: 0.06}
	p.Positioning["fear_greed"] = model.Indicator{Value: 85}

	v := BuildVerdict(p)
	if v.TopProximity < 0.6 {
		t.Fatalf("expected top proximity >= 0.6 with multiple top signals, got %.2f", v.TopProximity)
	}
}

func TestVerdictCoverageAffectsConfidence(t *testing.T) {
	p := newTestPanel()
	// 只有 1 个 available 指标，其余 missing
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7}
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Missing: true}
	p.Cycle["sma_200w_dev"] = model.Indicator{Missing: true}
	p.Valuation["ahr999"] = model.Indicator{Missing: true}
	p.Positioning["funding_rate_pct"] = model.Indicator{Missing: true}
	p.Macro["m2_yoy"] = model.Indicator{Missing: true}

	v := BuildVerdict(p)
	if v.Coverage > 0.5 {
		t.Fatalf("expected low coverage, got %.2f", v.Coverage)
	}
	if v.Confidence != "低（覆盖率不足）" {
		t.Fatalf("expected low-confidence label due to coverage, got %q", v.Confidence)
	}
}

func TestVerdictDedupValuationCluster(t *testing.T) {
	p := newTestPanel()
	// Cycle 域两个估值类指标看涨 + Valuation 域两个看涨 → 应该去重
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.6}
	p.Cycle["sma_200w_dev"] = model.Indicator{Value: -10}
	p.Valuation["ahr999"] = model.Indicator{Value: 0.3}
	p.Valuation["mvrv_z_score"] = model.Indicator{Value: -0.5}

	v := BuildVerdict(p)
	cycle := findDomain(v, "cycle")
	if cycle.Vote != 0 {
		t.Fatalf("cycle should be deduped to 0 since valuation 也同向，got %d", cycle.Vote)
	}
	if len(v.ClusterNotes) == 0 {
		t.Fatalf("expected a cluster note about dedup")
	}
}

func findDomain(v *Verdict, name string) DomainVote {
	for _, d := range v.Domains {
		if d.Domain == name {
			return d
		}
	}
	return DomainVote{}
}
