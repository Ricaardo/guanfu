// Per-asset extractor bundles. Each asset's BuildForecast calls its bundle
// to assemble the right mix of generic technicals + asset-specific macro.
//
// Design:
//   - Generic technicals (return/dd/RSI/vol/Mayer) work for any asset.
//   - Asset-specific macro extractors are constructed against PriceStore.
//   - If a macro source is missing, the extractor is silently dropped.
//   - BTC keeps its dedicated CoreExtractors() (halving/AHR are BTC-only).

package features

import (
	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/store"
)

// EquityExtractors returns the bundle for QQQ/SPY (US equities):
// generic technicals + valuation (CAPE) + rates (DGS10) + USD (DXY)
// + credit (HY spread) + curve (10Y-2Y) + risk (VIX).
func EquityExtractors(s *store.PriceStore) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractors()
	for _, ex := range []forecast.FeatureExtractor{
		CAPEExtractor(s),
		DGS10Extractor(s),
		DXYExtractor(s),
		HYSpreadExtractor(s),
		YieldCurveExtractor(s),
		VIXExtractor(s),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}

// GoldExtractors returns the bundle for London Gold:
// generic technicals + real yield (DFII10) + breakeven inflation
// + USD (DXY) + COT positioning + VIX (risk-off hedge).
func GoldExtractors(s *store.PriceStore) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractors()
	for _, ex := range []forecast.FeatureExtractor{
		RealYield10YExtractor(s),
		BreakevenExtractor(s),
		DXYExtractor(s),
		GoldCOTExtractor(s),
		VIXExtractor(s),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}

// HS300Extractors returns the bundle for HS300 (China A-shares):
// generic technicals + PMI + M2 + LPR + northbound flow.
func HS300Extractors(s *store.PriceStore) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractors()
	for _, ex := range []forecast.FeatureExtractor{
		PMIExtractor(s),
		M2Extractor(s),
		LPRExtractor(s),
		NorthboundExtractor(s),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}
