// Per-asset extractor bundles. Each asset's BuildForecast calls its bundle
// to assemble the right mix of generic technicals + asset-specific macro.
//
// Design:
//   - Generic technicals (return/dd/RSI/vol/Mayer) work for any asset.
//   - Asset-specific macro extractors are constructed against PriceStore.
//   - If a macro source is missing, the extractor is silently dropped.
//   - BTC keeps its dedicated CoreExtractors() (halving/AHR are BTC-only).
//   - Non-BTC assets inject per-profile normalization scales so that equity
//     and gold returns are not compressed by BTC's extreme-volatility defaults.

package features

import (
	"github.com/Ricaardo/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/store"
)

func ExtractorsForAsset(asset string, s *store.PriceStore) []forecast.FeatureExtractor {
	p, ok := assetprofile.For(asset)
	if !ok {
		return GenericTechnicalExtractors()
	}
	return ExtractorsForProfile(p, s)
}

func ExtractorsForProfile(p assetprofile.Profile, s *store.PriceStore) []forecast.FeatureExtractor {
	switch p.FeatureBundle {
	case "btc_core":
		return CoreExtractors()
	case "equity_index":
		return EquityExtractorsWithScales(s, PutCallAll, p.FeatureScales)
	case "gold":
		return GoldExtractorsWithScales(s, p.FeatureScales)
	case "us_stock":
		return USStockExtractorsWithScales(s, p.FeatureScales)
	default:
		return GenericTechnicalExtractors()
	}
}

// EquityExtractors returns the bundle for QQQ/SPY (US equities):
// generic technicals + valuation (CAPE) + rates (DGS10) + USD (DXY)
// + credit (HY spread) + curve (10Y-2Y) + risk (VIX).
func EquityExtractors(s *store.PriceStore) []forecast.FeatureExtractor {
	return EquityExtractorsWithPutCallMode(s, PutCallAll)
}

func EquityExtractorsWithPutCallMode(s *store.PriceStore, putCallMode PutCallFeatureMode) []forecast.FeatureExtractor {
	return EquityExtractorsWithScales(s, putCallMode, nil)
}

func EquityExtractorsWithScales(s *store.PriceStore, putCallMode PutCallFeatureMode, scales map[string]float64) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractorsWithScales(scales)
	for _, ex := range []forecast.FeatureExtractor{
		CAPEExtractor(s),
		DGS10Extractor(s),
		DXYExtractor(s),
		HYSpreadExtractor(s),
		YieldCurveExtractor(s),
		VIXExtractor(s),
		PutCallRatioExtractorWithMode(s, putCallMode),
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
	return GoldExtractorsWithScales(s, nil)
}

func GoldExtractorsWithScales(s *store.PriceStore, scales map[string]float64) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractorsWithScales(scales)
	for _, ex := range []forecast.FeatureExtractor{
		RealYield10YExtractor(s),
		BreakevenExtractor(s),
		DXYExtractor(s),       // USD 30d trend: DXY falling = gold bullish
		DGS10Extractor(s),     // 10Y rate 30d change: rising rates = gold headwind
		GoldCOTExtractor(s),
		VIXExtractor(s),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}

// USStockExtractors returns the bundle for arbitrary US stocks (D2).
// Same macro context as EquityExtractors but without CAPE — there's
// no per-name CAPE proxy for an arbitrary single stock, only for
// the broad indices that EquityExtractors targets.
//
// Bundle: generic technicals + DGS10 (rates) + DXY (USD)
// + HY spread (credit) + 10Y-2Y curve + VIX (risk-off).
func USStockExtractors(s *store.PriceStore) []forecast.FeatureExtractor {
	return USStockExtractorsWithScales(s, nil)
}

func USStockExtractorsWithScales(s *store.PriceStore, scales map[string]float64) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractorsWithScales(scales)
	for _, ex := range []forecast.FeatureExtractor{
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
