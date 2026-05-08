// China macro extractors for HS300 forecast.
// All draw from PriceStore with date-aligned forward-fill lookup.

package features

import (
	"math"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// PMIExtractor returns China manufacturing PMI level.
// >50 = expansion (bullish equities); <50 = contraction.
func PMIExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("hs300_pmi")
	if err != nil || len(pts) < 12 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDate(pts, byDate, points[i].Date)
		if v == 0 {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "pmi", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE((v-50)/3.0, 3)*1000) / 1000,
			Weight:     0.60, Note: "China manufacturing PMI",
		}}, true
	}
}

// M2Extractor returns China M2 YoY growth (%).
// Higher M2 growth = liquidity tailwind for equities.
func M2Extractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("hs300_m2")
	if err != nil || len(pts) < 12 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDate(pts, byDate, points[i].Date)
		if v == 0 {
			return nil, false
		}
		// Center around 9% (long-run avg), range 6-15
		return []forecast.FeatureValue{{
			Name: "m2_yoy", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE((v-9)/4.0, 3)*1000) / 1000,
			Weight:     0.50, Note: "M2 YoY growth",
		}}, true
	}
}

// LPRExtractor returns 1Y LPR rate level (inverted: lower = easier policy = bullish).
func LPRExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("hs300_lpr")
	if err != nil || len(pts) < 12 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDate(pts, byDate, points[i].Date)
		if v == 0 {
			return nil, false
		}
		// Center around 3.7%; lower = easier
		v2 := -(v - 3.7) / 1.0
		return []forecast.FeatureValue{{
			Name: "lpr_1y", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE(v2, 3)*1000) / 1000,
			Weight:     0.40, Note: "1Y LPR (inverted)",
		}}, true
	}
}

// NorthboundExtractor returns 30d cumulative net foreign inflows (亿 RMB).
// Positive = sustained foreign buying.
func NorthboundExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("hs300_northbound")
	if err != nil || len(pts) < 60 {
		return nil
	}
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		// Sum past 30 calendar days of northbound flow
		targetDate := points[i].Date
		sum := 0.0
		count := 0
		for j := len(pts) - 1; j >= 0 && count < 30; j-- {
			if pts[j].Date <= targetDate {
				sum += pts[j].Close
				count++
			}
		}
		if count < 5 {
			return nil, false
		}
		// Range typically -2000 to +2000 亿
		return []forecast.FeatureValue{{
			Name: "northbound_30d", Value: math.Round(sum),
			Normalized: math.Round(clipE(sum/1500, 3)*1000) / 1000,
			Weight:     0.45, Note: "30d cumulative northbound flow (亿)",
		}}, true
	}
}
