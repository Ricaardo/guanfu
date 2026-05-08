// CSI300 (沪深300) asset — China A-share large-cap index.
//
// Data source: Yahoo Finance 000300.SS (free, ~2015+).
// Domains: technical, valuation (PE/PB via Futu or estimated), macro (LPR, CNY).
//
// Simplified panel: technical + macro context.

package engine

import (
	"context"
	"fmt"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// HS300Asset implements Asset for CSI300.
type HS300Asset struct {
	store *store.PriceStore
}

func NewHS300Asset() *HS300Asset {
	return &HS300Asset{store: &store.PriceStore{}}
}

func (a *HS300Asset) Key() string  { return "hs300" }
func (a *HS300Asset) Name() string { return "CSI300 (沪深300)" }

func (a *HS300Asset) FetchSnapshot(ctx context.Context) (*AssetSnapshot, error) {
	history, err := a.store.LoadHistory("hs300")
	if err != nil || len(history) == 0 {
		return nil, fmt.Errorf("hs300: no price data in PriceStore — run Phase 0 CSI300 import first")
	}

	latest, _ := a.store.Latest("hs300")
	as := &AssetSnapshot{
		Asset:        "hs300",
		Date:         latest.Date,
		Price:        latest.Close,
		PriceAsOf:    latest.Date,
		PriceHistory: history,
	}
	return as, nil
}

func (a *HS300Asset) BuildPanel(as *AssetSnapshot) (*model.IndicatorPanel, error) {
	if len(as.PriceHistory) < 14 {
		return nil, fmt.Errorf("hs300: insufficient price history (%d days)", len(as.PriceHistory))
	}

	if len(as.PriceHistory) >= 200 {
		in := &HS300DashboardInput{
			Date: as.Date, Price: as.Price, PriceHistory: as.PriceHistory,
			PE: as.PE, PB: as.PB,
		}
		if as.CrossAssetPrices != nil {
			in.CNYUSD = as.CrossAssetPrices["cnyusd"]
		}
		// A4: pre-resolve China macro from PriceStore (zero values mean missing →
		// dashboard writes "待接入" placeholder).
		if pt, ok := a.store.Latest("hs300_cny"); ok && in.CNYUSD == 0 {
			in.CNYUSD = pt.Close
		}
		if pt, ok := a.store.Latest("hs300_pmi"); ok {
			in.PMI = pt.Close
		}
		if pt, ok := a.store.Latest("hs300_m2"); ok {
			in.M2YoY = pt.Close
		}
		if pt, ok := a.store.Latest("hs300_lpr"); ok {
			in.LPR1Y = pt.Close
		}
		if pt, ok := a.store.Latest("hs300_northbound"); ok {
			in.Northbound = pt.Close
		}
		return BuildHS300Dashboard(in), nil
	}

	in := &EquityPanelInput{
		Asset: "hs300", Date: as.Date, Price: as.Price,
		PriceAsOf: as.PriceAsOf, PriceHistory: as.PriceHistory,
	}
	return BuildEquityPanel(in), nil
}

func (a *HS300Asset) BuildVerdict(panel *model.IndicatorPanel) *Verdict {
	return BuildHS300Verdict(panel)
}

func (a *HS300Asset) BuildForecast(as *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	raw, err := a.store.Load("hs300")
	if err != nil {
		return nil, fmt.Errorf("hs300 forecast: load price store: %w", err)
	}
	if len(raw) < 200 {
		return nil, fmt.Errorf("hs300 forecast: need at least 200 days, got %d", len(raw))
	}
	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: "price_store:hs300"}
	}
	if len(opts.Horizons) == 0 {
		opts = forecast.DefaultOptions()
	}
	opts.Extractors = features.HS300Extractors(a.store)
	return forecast.Build(points, opts)
}

func init() {
	Register(NewHS300Asset())
}
