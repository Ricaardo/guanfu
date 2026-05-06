// QQQ Asset — Nasdaq-100 ETF panel via shared equity builder.

package engine

import (
	"context"
	"fmt"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// QQAsset implements Asset for QQQ (Nasdaq-100 ETF).
type QQAsset struct {
	store *store.PriceStore
}

// NewQQAsset creates the QQQ asset implementation.
func NewQQAsset() *QQAsset {
	return &QQAsset{store: &store.PriceStore{}}
}

func (a *QQAsset) Key() string  { return "qqq" }
func (a *QQAsset) Name() string { return "Nasdaq-100 ETF" }

func (a *QQAsset) FetchSnapshot(ctx context.Context) (*AssetSnapshot, error) {
	history, err := a.store.LoadHistory("qqq")
	if err != nil || len(history) == 0 {
		return nil, fmt.Errorf("qqq: no price data in PriceStore — run Phase 0 Futu import first")
	}

	latest, _ := a.store.Latest("qqq")
	vixy, _ := a.store.Latest("vixy")
	uup, _ := a.store.Latest("uup")
	tlt, _ := a.store.Latest("tlt")
	gold, _ := a.store.Latest("gold")

	as := &AssetSnapshot{
		Asset:        "qqq",
		Date:         latest.Date,
		Price:        latest.Close,
		PriceAsOf:    latest.Date,
		PriceHistory: history,
		CrossAssetPrices: map[string]float64{
			"vixy": vixy.Close,
			"uup":  uup.Close,
			"tlt":  tlt.Close,
			"gold": gold.Close,
		},
	}

	return as, nil
}

func (a *QQAsset) BuildPanel(as *AssetSnapshot) (*model.IndicatorPanel, error) {
	if len(as.PriceHistory) < 14 {
		return nil, fmt.Errorf("qqq: insufficient price history (%d days)", len(as.PriceHistory))
	}

	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         as.Date,
		Price:        as.Price,
		PriceAsOf:    as.PriceAsOf,
		PriceHistory: as.PriceHistory,
		PE:           as.PE,
		PB:           as.PB,
	}
	if as.CrossAssetPrices != nil {
		in.VIX = as.CrossAssetPrices["vixy"]
		in.DXY = as.CrossAssetPrices["uup"]
		in.TLT = as.CrossAssetPrices["tlt"]
		in.Gold = as.CrossAssetPrices["gold"]
	}

	// Use enhanced dashboard (if enough history)
	if len(as.PriceHistory) >= 200 {
		din := &EquityDashboardInput{
			Asset: "qqq", Date: as.Date, Price: as.Price,
			PriceHistory: as.PriceHistory, PE: as.PE, PB: as.PB,
		}
		if as.CrossAssetPrices != nil {
			din.VIX = as.CrossAssetPrices["vixy"]
			din.DXY = as.CrossAssetPrices["uup"]
			din.TLT = as.CrossAssetPrices["tlt"]
		}
		panel := BuildEquityDashboard(din)
		enrichEquityPanelWithValuation(panel, "qqq", as.PE, as.PB)
		return panel, nil
	}
	panel := BuildEquityPanel(in)
	enrichEquityPanelWithValuation(panel, "qqq", as.PE, as.PB)
	return panel, nil
}

// TryFetchValuation attempts to populate PE/PB via Futu snapshot.
func (a *QQAsset) TryFetchValuation(as *AssetSnapshot) {
	v := tryFetchEquityValuation()
	if v != nil && v.QQQPE > 0 {
		as.PE = v.QQQPE
		as.PB = v.QQQPB
	}
}

func (a *QQAsset) BuildVerdict(panel *model.IndicatorPanel) *Verdict {
	return BuildEquityVerdict(panel)
}

func (a *QQAsset) BuildForecast(as *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	if len(as.PriceHistory) < 200 {
		return nil, fmt.Errorf("qqq forecast: need at least 200 days, got %d", len(as.PriceHistory))
	}
	points := make([]forecast.Point, len(as.PriceHistory))
	for i, p := range as.PriceHistory {
		points[len(as.PriceHistory)-1-i] = forecast.Point{
			Date:  as.Date,
			Close: p,
			Source: "price_store:qqq",
		}
	}
	if len(opts.Horizons) == 0 {
		opts = forecast.DefaultOptions()
	}
	opts.Extractors = features.CoreExtractors()
	return forecast.Build(points, opts)
}

func init() {
	Register(NewQQAsset())
	Register(NewSPYAsset())
}
