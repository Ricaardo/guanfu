package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/internal/model"

	"github.com/shopspring/decimal"
)

// TestBuildPanelAllDomainsNonEmpty verifies that a minimal valid snapshot
// produces a panel where all 8 domain maps are non-nil.
func TestBuildPanelAllDomainsNonEmpty(t *testing.T) {
	snap := &model.MarketSnapshot{
		Date:                    time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		SnapshotSchemaVersion:   model.CurrentMarketSnapshotSchemaVersion,
		BTCPrice:                decimal.NewFromInt(80000),
		ETHPrice:                decimal.NewFromInt(2500),
		BTCDominance:            decimal.NewFromFloat(0.55),
		TotalMarketCap:          decimal.NewFromFloat(1.5e12),
		FearGreedIndex:          decimal.NewFromInt(50),
		AltcoinSeasonIndex:      decimal.NewFromInt(35),
		BTCFundingRate:          decimal.NewFromFloat(0.0001),
		BTCOpenInterest:         decimal.NewFromFloat(500000),
		OnchainValuationFetched: true,
		CapMVRVCur:              decimal.NewFromFloat(2.0),
		MVRVZScore:              decimal.NewFromFloat(1.5),
		NUPL:                    decimal.NewFromFloat(0.5),
		HashRateEHs:             decimal.NewFromFloat(600),
		HashRibbonsLabel:        "上行（矿工扩张）",
		DifficultyChangePct:     decimal.NewFromFloat(2.0),
		MempoolMB:               decimal.NewFromFloat(20),
		ETFNetFlow7dUSD:         decimal.NewFromFloat(5e8),
		ETFNetFlow30dUSD:        decimal.NewFromFloat(2e9),
		ETFTotalAssetUSD:        decimal.NewFromFloat(1e11),
		CrossAssetFetched:       true,
		GoldPriceUSD:            decimal.NewFromInt(4500),
		QQQPrice:                decimal.NewFromInt(500),
		SPYPrice:                decimal.NewFromInt(550),
	}

	// Generate synthetic history (1000 days, enough for all calculations)
	n := 1000
	snap.BTCPriceHistory = make([]decimal.Decimal, n)
	snap.ETHPriceHistory = make([]decimal.Decimal, n)
	snap.GoldHistory = make([]decimal.Decimal, n)
	snap.QQQHistory = make([]decimal.Decimal, n)
	snap.SPYHistory = make([]decimal.Decimal, n)
	for i := 0; i < n; i++ {
		p := 80000.0 * (1 + float64(n-i)/float64(n)*0.5)
		snap.BTCPriceHistory[i] = decimal.NewFromFloat(p)
		snap.ETHPriceHistory[i] = decimal.NewFromFloat(p * 0.03)
		snap.GoldHistory[i] = decimal.NewFromFloat(4500.0)
		snap.QQQHistory[i] = decimal.NewFromFloat(500.0)
		snap.SPYHistory[i] = decimal.NewFromFloat(550.0)
	}

	calc := NewCalculator(&model.Config{})
	panel := calc.BuildPanel(snap)

	// Verify all 8 domains exist
	domains := map[string]map[string]model.Indicator{
		"cycle":       panel.Cycle,
		"valuation":   panel.Valuation,
		"network":     panel.Network,
		"positioning": panel.Positioning,
		"macro":       panel.Macro,
		"flow":        panel.Flow,
		"technical":   panel.Technical,
		"cross_asset": panel.CrossAsset,
	}

	for name, dom := range domains {
		if dom == nil {
			t.Errorf("domain %q is nil", name)
		}
		if len(dom) == 0 {
			t.Errorf("domain %q is empty", name)
		}
	}

	// Verify JSON marshaling
	b, err := json.Marshal(panel)
	if err != nil {
		t.Fatalf("json.Marshal panel: %v", err)
	}
	if len(b) < 100 {
		t.Fatalf("panel JSON too short: %d bytes", len(b))
	}

	// Check key indicators are present
	if _, ok := panel.Technical["rsi_14"]; !ok {
		t.Error("technical.rsi_14 missing")
	}
	if _, ok := panel.CrossAsset["btc_gold_ratio"]; !ok {
		t.Error("cross_asset.btc_gold_ratio missing")
	}
	if _, ok := panel.Cycle["mayer_multiple"]; !ok {
		t.Error("cycle.mayer_multiple missing")
	}

	t.Logf("Panel OK: %d bytes JSON, 8 domains populated", len(b))
}
