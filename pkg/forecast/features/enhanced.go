// Enhanced feature extractors: CAPE valuation, daily macro, cross-asset lead-lag.
// All use PriceStore for date-aligned data lookup.

package features

import (
	"math"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// ─── CAPE (Shiller PE) valuation ────────────────────────

// CAPEExtractor returns a feature that looks up Shiller CAPE from PriceStore.
// CAPE values: typical range 10-45, median ~20. Higher = more expensive.
func CAPEExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	capePts, err := s.Load("spx_cape")
	if err != nil || len(capePts) < 10 {
		return nil
	}
	// Build sorted lookup array
	capeByDate := makeDateMap(capePts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDate(capePts, capeByDate, points[i].Date)
		if v <= 0 {
			return nil, false
		}
		// Log-normalize: log(CAPE/20) / 0.6
		normalized := (math.Log(v) - math.Log(20)) / 0.6
		return []forecast.FeatureValue{{
			Name: "cape", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE(normalized, 3)*1000) / 1000,
			Weight: 0.55, Note: "Shiller CAPE (monthly)",
		}}, true
	}
}

// ─── Daily Macro: DGS10 + DXY ───────────────────────────

// DGS10Extractor returns 10Y Treasury rate 30d change.
func DGS10Extractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_dgs10")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		now := lookupDate(pts, byDate, points[i].Date)
		past := lookupDate(pts, byDate, points[i-30].Date)
		if now == 0 || past == 0 {
			return nil, false
		}
		chg := (now - past) / 0.5
		return []forecast.FeatureValue{{
			Name: "dgs10_30d", Value: math.Round((now-past)*10000) / 100,
			Normalized: math.Round(clipE(chg, 3)*1000) / 1000,
			Weight: 0.45, Note: "10Y Treasury 30d change",
		}}, true
	}
}

// DXYExtractor returns Trade-Weighted USD 30d trend.
func DXYExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_dxy")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		now := lookupDate(pts, byDate, points[i].Date)
		past := lookupDate(pts, byDate, points[i-30].Date)
		if now == 0 || past == 0 {
			return nil, false
		}
		chg := (now-past)/past / 0.03
		return []forecast.FeatureValue{{
			Name: "dxy_30d", Value: math.Round((now/past-1)*10000) / 100,
			Normalized: math.Round(clipE(-chg, 3)*1000) / 1000,
			Weight: 0.45, Note: "USD 30d trend (inverted)",
		}}, true
	}
}

// ─── Cross-asset Lead-Lag ───────────────────────────────

// LeadLagExtractor computes if this asset leads/lags another at 7d lag.
// otherAsset: "qqq", "gold", "btc" etc.
func LeadLagExtractor(s *store.PriceStore, otherAsset string, lagDays, window int) forecast.FeatureExtractor {
	otherPts, err := s.Load(otherAsset)
	if err != nil || len(otherPts) < 200 {
		return nil
	}
	otherByDate := makeDateMap(otherPts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < window+lagDays {
			return nil, false
		}
		// Compute rolling correlation: this_asset[t-lag] vs other_asset[t]
		var thisRets, otherRets []float64
		for j := i - window + 1; j <= i-lagDays; j++ {
			if points[j-1].Close <= 0 || points[j].Close <= 0 {
				continue
			}
			thisRet := math.Log(points[j].Close / points[j-1].Close)
			otherNow := lookupDate(otherPts, otherByDate, points[j].Date)
			otherPast := lookupDate(otherPts, otherByDate, points[j-1].Date)
			if otherNow <= 0 || otherPast <= 0 {
				continue
			}
			otherRet := math.Log(otherNow / otherPast)
			thisRets = append(thisRets, thisRet)
			otherRets = append(otherRets, otherRet)
		}
		if len(thisRets) < 15 {
			return nil, false
		}
		corr := pearsonE(thisRets, otherRets)
		if math.IsNaN(corr) {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "leadlag_7d", Value: math.Round(corr*1000) / 1000,
			Normalized: math.Round(clipE(corr, 3)*1000) / 1000,
			Weight: 0.35, Note: "Cross-asset 7d lead correlation",
		}}, true
	}
}

// ─── Helpers ─────────────────────────────────────────────

func makeDateMap(pts []store.PricePoint) map[string]float64 {
	m := make(map[string]float64, len(pts))
	for _, p := range pts {
		m[p.Date] = p.Close
	}
	return m
}

func lookupDate(pts []store.PricePoint, dateMap map[string]float64, date string) float64 {
	// Fast path: exact match
	if v, ok := dateMap[date]; ok {
		return v
	}
	// Forward-fill: find most recent before date
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i].Date <= date {
			return pts[i].Close
		}
	}
	return 0
}

func pearsonE(x, y []float64) float64 {
	n := float64(len(x))
	var sx, sy, sxy, sx2, sy2 float64
	for i := range x {
		sx += x[i]
		sy += y[i]
		sxy += x[i] * y[i]
		sx2 += x[i] * x[i]
		sy2 += y[i] * y[i]
	}
	num := n*sxy - sx*sy
	den := math.Sqrt((n*sx2 - sx*sx) * (n*sy2 - sy*sy))
	if den == 0 {
		return 0
	}
	return num / den
}

func clipE(v, mx float64) float64 {
	if v > mx {
		return mx
	}
	if v < -mx {
		return -mx
	}
	return v
}
