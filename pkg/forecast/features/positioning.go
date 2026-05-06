// Positioning / sentiment feature extractors.
//
// These features depend on data sources that do NOT have historical archives
// accessible through free APIs. They are available for current-date extraction
// only (via live API calls during GetSnapshot).
//
// Historical pipeline (Phase 4 late):
//   - Fear & Greed: alternative.me ?limit=0 (2018+ full history) ✓ in Phase 0
//   - Funding rate: Binance historical funding (2019+)
//   - OI/MC: Binance historical OI (2019+)
//   - Stablecoin 30d change: CoinGecko range API

package features

import (
	"github.com/Ricaardo/guanfu/pkg/forecast"
)

// PositioningExtractors returns extractors that work for the current date only.
// data is a map of indicator name → current value (e.g. "fear_greed" → 55.0).
func PositioningExtractors(data map[string]float64) []forecast.FeatureExtractor {
	if len(data) == 0 {
		return nil
	}

	// These extractors ignore the points parameter — they just return the current value
	// wrapped as a "feature" that's valid for the most recent index.
	return []forecast.FeatureExtractor{
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			// Only valid for the latest point (current date)
			if i < len(points)-3 {
				return nil, false
			}
			fg, ok := data["fear_greed"]
			if !ok || fg < 0 || fg > 100 {
				return nil, false
			}
			return []forecast.FeatureValue{{
				Name: "fear_greed", Value: fg,
				Normalized: (fg - 50) / 35, Weight: 0.40,
			}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < len(points)-3 {
				return nil, false
			}
			fr, ok := data["funding_rate_pct"]
			if !ok {
				return nil, false
			}
			return []forecast.FeatureValue{{
				Name: "funding_rate_pct", Value: fr,
				Normalized: fr / 0.10, Weight: 0.35,
			}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < len(points)-3 {
				return nil, false
			}
			oi, ok := data["oi_to_mc_pct"]
			if !ok {
				return nil, false
			}
			return []forecast.FeatureValue{{
				Name: "oi_to_mc_pct", Value: oi,
				Normalized: (oi - 2) / 3, Weight: 0.30,
			}}, true
		},
	}
}
