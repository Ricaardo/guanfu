// Gold asset — London gold (XAU/USD) panel.
//
// Domains:
//   - technical: RSI, MACD, SMA(50/200), BB, volatility (same as equities)
//   - valuation: real yield proxy (TLT trend), DXY direction, VIX level
//   - macro: VIX level, DXY/UUP price
//
// Data sources:
//   - PriceStore gold.json (London gold pipeline, DBnomics 1968+ / Yahoo XAUUSD=X)
//   - PriceStore vixy.json, uup.json, tlt.json (cross-asset context)
//   - FRED DFII10 (real yield, when available)

package engine

import (
	"context"
	"fmt"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// GoldAsset implements Asset for London Gold (XAU/USD).
type GoldAsset struct {
	store *store.PriceStore
}

// NewGoldAsset creates the gold asset implementation.
func NewGoldAsset() *GoldAsset {
	return &GoldAsset{store: &store.PriceStore{}}
}

func (a *GoldAsset) Key() string  { return "gold" }
func (a *GoldAsset) Name() string { return "London Gold (XAU/USD)" }

func (a *GoldAsset) FetchSnapshot(ctx context.Context) (*AssetSnapshot, error) {
	history, err := a.store.LoadHistory("gold")
	if err != nil || len(history) == 0 {
		return nil, fmt.Errorf("gold: no price data in PriceStore — run Phase 0 gold pipeline first")
	}

	latest, _ := a.store.Latest("gold")
	vixy, _ := a.store.Latest("vixy")
	uup, uupOK := a.store.Latest("uup")
	tlt, _ := a.store.Latest("tlt")

	// A3: fall back to FRED trade-weighted USD when UUP ETF data is missing —
	// keeps the existing "uup" key so BuildEquityPanel's dxy_proxy path still fires.
	var dxy float64
	if uupOK {
		dxy = uup.Close
	}
	if !uupOK || dxy == 0 {
		if fredDxy, ok := a.store.Latest("fred_dxy"); ok {
			dxy = fredDxy.Close
		}
	}

	as := &AssetSnapshot{
		Asset:        "gold",
		Date:         latest.Date,
		Price:        latest.Close,
		PriceAsOf:    latest.Date,
		PriceHistory: history,
		CrossAssetPrices: map[string]float64{
			"vixy": vixy.Close,
			"uup":  dxy,
			"tlt":  tlt.Close,
		},
		RealYield10Y: 0, // populated if FRED data is available
		DXY:          dxy,
		VIX:          vixy.Close,
	}

	return as, nil
}

func (a *GoldAsset) BuildPanel(as *AssetSnapshot) (*model.IndicatorPanel, error) {
	if len(as.PriceHistory) < 14 {
		return nil, fmt.Errorf("gold: insufficient price history (%d days)", len(as.PriceHistory))
	}

	// Reuse equity panel builder for technical indicators
	in := &EquityPanelInput{
		Asset:        "gold",
		Date:         as.Date,
		Price:        as.Price,
		PriceAsOf:    as.PriceAsOf,
		PriceHistory: as.PriceHistory,
	}
	if as.CrossAssetPrices != nil {
		in.VIX = as.CrossAssetPrices["vixy"]
		in.DXY = as.CrossAssetPrices["uup"]
		in.TLT = as.CrossAssetPrices["tlt"]
	}

	panel := BuildEquityPanel(in)

	// ── Gold valuation domain ──
	panel.Valuation = buildGoldValuation(as, panel.Macro["tlt_proxy"])
	EnrichGlobalInvestorMacro(panel, a.store)

	return panel, nil
}

func (a *GoldAsset) BuildVerdict(panel *model.IndicatorPanel) *Verdict {
	// Extend equity verdict with gold-specific valuation
	v := BuildEquityVerdict(panel)

	// Add valuation domain vote
	valVote, valBull, valBear := scoreGoldValuation(panel.Valuation)
	v.Domains = append(v.Domains, DomainVote{
		Domain:   "valuation",
		Vote:     valVote,
		Bullish:  valBull,
		Bearish:  valBear,
		Coverage: coverageScore(panel.Valuation),
	})

	v.NetDirection += valVote

	// Recompute coverage
	totalCoverage := 0.0
	for _, d := range v.Domains {
		totalCoverage += d.Coverage
	}
	if len(v.Domains) > 0 {
		v.Coverage = totalCoverage / float64(len(v.Domains))
	}

	// Gold-specific stance
	switch {
	case v.NetDirection >= 3:
		v.Regime = "偏强积累区"
	case v.NetDirection <= -3:
		v.Regime = "偏弱谨慎区"
	default:
		v.Regime = "中性"
	}

	return v
}

func (a *GoldAsset) BuildForecast(as *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	raw, err := a.store.Load("gold")
	if err != nil {
		return nil, fmt.Errorf("gold forecast: load price store: %w", err)
	}
	if len(raw) < 200 {
		return nil, fmt.Errorf("gold forecast: need at least 200 days, got %d", len(raw))
	}
	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: "price_store:gold"}
	}
	if len(opts.Horizons) == 0 {
		opts.Horizons = forecast.HorizonsForAsset("gold")
	}
	opts.Asset = "gold"
	opts.Extractors = features.ExtractorsForAsset("gold", a.store)
	return forecast.Build(points, opts)
}

// ─── Gold valuation domain ───────────────────────────────

func buildGoldValuation(as *AssetSnapshot, tltProxy model.Indicator) map[string]model.Indicator {
	m := make(map[string]model.Indicator)

	// TLT proxy for real yield: TLT rising ≈ real yields falling ≈ gold bullish
	if tltProxy.IsAvailable() && len(as.PriceHistory) >= 60 {
		// Compute 60d TLT trend from history if available
		tltTrend := computeTLTTrendFromStore(as)
		m["real_yield_proxy"] = model.Indicator{
			Value:  tltTrend,
			Label:  goldRealYieldLabel(tltTrend),
			Source: "price_store:tlt",
		}
	}

	// DXY/UUP direction
	if as.DXY > 0 && len(as.PriceHistory) >= 60 {
		dxyLabel := goldDXYLabel(as.DXY)
		m["dxy_level"] = model.Indicator{
			Value:  as.DXY,
			Label:  dxyLabel,
			Source: "price_store:uup",
		}
	}

	// VIX level
	if as.VIX > 0 {
		m["vix_level"] = model.Indicator{
			Value:  as.VIX,
			Label:  goldVIXLabel(as.VIX),
			Source: "price_store:vixy",
		}
	}

	return m
}

func computeTLTTrendFromStore(as *AssetSnapshot) float64 {
	// Use TLT price from cross-asset context
	tlt := as.CrossAssetPrices["tlt"]
	if tlt <= 0 {
		return 0
	}
	return tlt
}

func goldRealYieldLabel(tltPrice float64) string {
	// TLT > 92 ≈ real yields declining
	if tltPrice > 100 {
		return "实际利率下行 (利好黄金)"
	} else if tltPrice > 92 {
		return "实际利率中性偏低"
	} else if tltPrice > 85 {
		return "实际利率中性"
	} else {
		return "实际利率偏高 (压制黄金)"
	}
}

func goldDXYLabel(dxy float64) string {
	if dxy > 30 {
		return "美元偏强 (压制黄金)"
	} else if dxy < 25 {
		return "美元偏弱 (利好黄金)"
	}
	return "美元中性"
}

func goldVIXLabel(vix float64) string {
	switch {
	case vix > 30:
		return "恐慌→避险需求 (利好黄金)"
	case vix > 20:
		return "偏高→温和避险"
	default:
		return "低波动→风险偏好"
	}
}

func scoreGoldValuation(v map[string]model.Indicator) (vote int, bull, bear []string) {
	if ryp, ok := v["real_yield_proxy"]; ok && ryp.IsAvailable() {
		if ryp.Value > 100 {
			bull = append(bull, "实际利率下行")
			vote++
		} else if ryp.Value < 85 {
			bear = append(bear, "实际利率偏高")
			vote--
		}
	}
	if vix, ok := v["vix_level"]; ok && vix.IsAvailable() {
		if vix.Value > 30 {
			bull = append(bull, fmt.Sprintf("VIX恐慌(%.0f)→避险", vix.Value))
			vote++
		} else if vix.Value < 12 {
			bear = append(bear, "VIX极低→无避险需求")
			vote--
		}
	}
	return
}

func init() {
	Register(NewGoldAsset())
}
