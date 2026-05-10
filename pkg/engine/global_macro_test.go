package engine

import (
	"testing"

	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/Ricaardo/guanfu/pkg/store"
)

func TestEnrichGlobalInvestorMacroAddsFXAndCentralBankContext(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	if err := ps.Save("usd_cny", []store.PricePoint{
		{Date: "2026-01-01", Close: 7.00, Source: "yahoo:CNY=X"},
		{Date: "2026-03-05", Close: 7.21, Source: "yahoo:CNY=X"},
	}); err != nil {
		t.Fatal(err)
	}
	for key, v := range map[string]float64{
		"fred_fed_funds":           4.25,
		"fred_ecb_deposit_rate":    2.00,
		"fred_boj_call_rate":       0.73,
		"fred_pboc_interbank_rate": 2.90,
	} {
		if err := ps.Save(key, []store.PricePoint{{Date: "2026-03-01", Close: v, Source: "fred:test"}}); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}

	panel := &model.IndicatorPanel{Macro: map[string]model.Indicator{}}
	EnrichGlobalInvestorMacro(panel, ps)

	if got := panel.Macro["usd_cny"]; got.Value != 7.21 || got.Missing {
		t.Fatalf("unexpected usd_cny indicator: %+v", got)
	}
	if got := panel.Macro["usd_cny_60d_trend_pct"]; got.Value != 3 || got.Missing {
		t.Fatalf("unexpected usd_cny trend: %+v", got)
	}
	if got := panel.Macro["global_rate_us_fed_pct"]; got.Value != 4.25 || got.Missing {
		t.Fatalf("unexpected Fed rate: %+v", got)
	}
	if got := panel.Macro["global_rate_spread_us_cn_pct"]; got.Value != 1.35 || got.Missing {
		t.Fatalf("unexpected US-CN spread: %+v", got)
	}
	if got := panel.Macro["global_dm_policy_rate_avg_pct"]; got.Value != 2.33 || got.Missing {
		t.Fatalf("unexpected DM policy avg: %+v", got)
	}

	if got, ok := testSourceHealth(panel.SourceHealth, investorFXSourceName); !ok || got.Status != "ok" || got.AsOf != "2026-03-05" {
		t.Fatalf("unexpected FX source health ok=%v health=%+v", ok, got)
	}
	if got, ok := testSourceHealth(panel.SourceHealth, globalCentralBankRateSource); !ok || got.Status != "ok" || got.AsOf != "2026-03-01" {
		t.Fatalf("unexpected central bank source health ok=%v health=%+v", ok, got)
	}
}

func TestEnrichGlobalInvestorMacroFallsBackToTBillWhenFedFundsMissing(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	if err := ps.Save("fred_dgs3mo", []store.PricePoint{{Date: "2026-03-01", Close: 4.10, Source: "fred:DGS3MO"}}); err != nil {
		t.Fatal(err)
	}

	panel := &model.IndicatorPanel{}
	EnrichGlobalInvestorMacro(panel, ps)

	got, ok := panel.Macro["global_rate_us_fed_pct"]
	if !ok || got.Value != 4.10 || got.Source != "fred:DGS3MO" {
		t.Fatalf("expected DGS3MO fallback, got ok=%v ind=%+v", ok, got)
	}

	health, ok := testSourceHealth(panel.SourceHealth, globalCentralBankRateSource)
	if !ok || health.Status != "partial" || health.AsOf != "2026-03-01" {
		t.Fatalf("expected partial central-bank source health, got ok=%v health=%+v", ok, health)
	}
}

func testSourceHealth(items []model.SourceHealth, source string) (model.SourceHealth, bool) {
	for _, item := range items {
		if item.Source == source {
			return item, true
		}
	}
	return model.SourceHealth{}, false
}
