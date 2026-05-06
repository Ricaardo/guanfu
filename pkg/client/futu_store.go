// futu_store.go — bridge between Futu data fetching and PriceStore.
//
// Provides generic fetching functions that return store.PricePoint slices,
// consumable by the PriceStore migration and incremental update logic.

package client

import (
	"fmt"

	"github.com/Ricaardo/guanfu/pkg/store"
)

// FetchFutuPricePoints fetches daily close prices for multiple Futu symbols
// and returns them as a map of lowercase-asset-key → oldest-first PricePoint slice.
// Uses the Python bridge (or direct Go client when available).
func FetchFutuPricePoints(symbols []string, days int) (map[string][]store.PricePoint, error) {
	if days <= 0 {
		days = 3000
	}

	c, err := futuConnect(futuAddr())
	if err != nil {
		// Fall back to Python bridge
		return futuBridgePricePoints(symbols, days)
	}
	defer c.Close()

	result := make(map[string][]store.PricePoint, len(symbols))
	for _, sym := range symbols {
		kl, err := c.RequestHistoryKL(sym, days)
		if err != nil {
			continue
		}
		if len(kl) == 0 {
			continue
		}
		normalizeFutuKLNewestFirst(kl)
		asset := futuSymbolToAsset(sym)
		points := make([]store.PricePoint, len(kl))
		// Reverse: oldest-first for PriceStore
		for i, k := range kl {
			points[len(kl)-1-i] = store.PricePoint{
				Date:   k.Time.Format("2006-01-02"),
				Close:  k.Close,
				Source: fmt.Sprintf("futu:%s", sym),
			}
		}
		result[asset] = points
	}
	return result, nil
}

// futuBridgePricePoints fetches via Python bridge and converts to PricePoint slices.
func futuBridgePricePoints(symbols []string, days int) (map[string][]store.PricePoint, error) {
	bridgeResult, err := futuBridgeSymbols(symbols, days)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]store.PricePoint)
	// Bridge result uses the CrossAssetFutuPrices struct — unpack each symbol
	unpackFutuResult(bridgeResult, symbols, result)
	return result, nil
}

// unpackFutuResult extracts price history from CrossAssetFutuPrices into PricePoint map.
func unpackFutuResult(r *CrossAssetFutuPrices, symbols []string, dst map[string][]store.PricePoint) {
	for _, sym := range symbols {
		price, history, asOf := futuFieldBySymbol(r, sym)
		if len(history) == 0 || price <= 0 {
			continue
		}
		asset := futuSymbolToAsset(sym)
		points := make([]store.PricePoint, len(history))
		// history is newest-first; convert to oldest-first
		for i, p := range history {
			date := ""
			if i == 0 && asOf != "" {
				date = asOf
			}
			points[len(history)-1-i] = store.PricePoint{
				Date:   date,
				Close:  p,
				Source: fmt.Sprintf("futu:%s", sym),
			}
		}
		dst[asset] = points
	}
}

func futuFieldBySymbol(r *CrossAssetFutuPrices, sym string) (float64, []float64, string) {
	switch sym {
	case "US.QQQ":
		return r.QQQPrice, r.QQQHistory, r.QQQPriceAsOf
	case "US.SPY":
		return r.SPYPrice, r.SPYHistory, r.SPYPriceAsOf
	case "US.GLD":
		return r.GLDPrice, r.GLDHistory, r.GLDPriceAsOf
	case "US.UUP":
		return r.UUPPrice, r.UUPHistory, r.UUPPriceAsOf
	case "US.TLT":
		return r.TLTPrice, r.TLTHistory, r.TLTPriceAsOf
	case "US.VIXY":
		return r.VIXYPrice, r.VIXYHistory, r.VIXYPriceAsOf
	case "US.USO":
		return r.WTIPrice, r.WTIHistory, r.WTIPriceAsOf
	case "US.BIL":
		return r.BILPrice, r.BILHistory, r.BILPriceAsOf
	case "US.SHY":
		return r.SHYPrice, r.SHYHistory, r.SHYPriceAsOf
	case "US.BND":
		return r.BNDPrice, r.BNDHistory, r.BNDPriceAsOf
	case "US.VTI":
		return r.VTIPrice, r.VTIHistory, r.VTIPriceAsOf
	default:
		return 0, nil, ""
	}
}

func futuSymbolToAsset(sym string) string {
	// "US.QQQ" -> "qqq", "US.USO" -> "wti"
	code := sym
	if len(sym) > 3 && sym[:3] == "US." {
		code = sym[3:]
	}
	switch code {
	case "USO":
		return "wti"
	case "GLD":
		return "gld"
	default:
		return code
	}
}
