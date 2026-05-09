// Native-Go refresh wrappers around the existing fetchers in this package.
//
// BTC: LoadOrUpdateBTCDailyHistory already manages its own JSON cache, but
// we also mirror the result into PriceStore("btc") so the unified status
// table sees one source of truth.
//
// Gold: FetchGoldIncremental(lastDate) returns oldest-first PricePoint
// for the gap. On full import (lastDate=""), FetchLondonGoldPricePoints
// returns the DBnomics+Yahoo merge from 1968+.
//
// HS300: FetchHS300Incremental / FetchHS300PricePoints already targets
// store.PricePoint and writes to "hs300".

package client

import (
	"context"
	"fmt"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

// BTCSource refreshes the canonical BTC daily price archive.
type BTCSource struct{}

func (BTCSource) Key() string         { return "btc" }
func (BTCSource) DisplayName() string { return "btc (CoinMetrics + Binance)" }

func (BTCSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, "btc")
	if !stale {
		return freshSkipResult("btc", "btc (CoinMetrics + Binance)", lastDate, s), nil
	}
	preCount, _ := s.Count("btc")

	points, err := LoadOrUpdateBTCDailyHistory(ctx, "")
	if err != nil {
		return nil, err
	}

	// Mirror to PriceStore("btc"). Save (full-replace) is fine because
	// LoadOrUpdateBTCDailyHistory always returns the canonical full series.
	pps := make([]store.PricePoint, 0, len(points))
	for _, p := range points {
		c, _ := p.Close.Float64()
		if c <= 0 {
			continue
		}
		pps = append(pps, store.PricePoint{
			Date:   p.Date,
			Close:  c,
			Source: p.Source,
		})
	}
	if err := s.Save("btc", pps); err != nil {
		return nil, err
	}

	count, _ := s.Count("btc")
	last, _ := s.LastDate("btc")
	mode := "full"
	added := count
	if preCount > 0 {
		mode = "incremental"
		added = count - preCount
		if added < 0 {
			added = 0 // happens if BTC source dropped a duplicate row
		}
	}
	return &RefreshResult{
		Key: "btc", DisplayName: "btc (CoinMetrics + Binance)",
		Mode: mode, Added: added, Total: count, LastDate: last,
	}, nil
}

// GoldSource refreshes London Gold via DBnomics (full) + Yahoo (incremental).
type GoldSource struct{}

func (GoldSource) Key() string         { return "gold" }
func (GoldSource) DisplayName() string { return "gold (DBnomics + Yahoo)" }

func (GoldSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, "gold")
	if !stale {
		return freshSkipResult("gold", "gold (DBnomics + Yahoo)", lastDate, s), nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}

	var pts []store.PricePoint
	var err error
	if mode == "incremental" {
		pts, err = FetchGoldIncremental(ctx, lastDate)
	} else {
		pts, err = FetchLondonGoldPricePoints(ctx)
	}
	if err != nil {
		return nil, err
	}
	if len(pts) == 0 {
		count, _ := s.Count("gold")
		return &RefreshResult{
			Key: "gold", DisplayName: "gold (DBnomics + Yahoo)",
			Mode: mode, Added: 0, Total: count, LastDate: lastDate,
		}, nil
	}

	if mode == "full" {
		if err := s.Save("gold", pts); err != nil {
			return nil, err
		}
	} else {
		if err := s.Append("gold", pts); err != nil {
			return nil, err
		}
	}
	count, _ := s.Count("gold")
	last, _ := s.LastDate("gold")
	return &RefreshResult{
		Key: "gold", DisplayName: "gold (DBnomics + Yahoo)",
		Mode: mode, Added: len(pts), Total: count, LastDate: last,
	}, nil
}

// HS300Source refreshes CSI300 via Yahoo (full / incremental).
type HS300Source struct{}

func (HS300Source) Key() string         { return "hs300" }
func (HS300Source) DisplayName() string { return "hs300 (Yahoo ^HS300)" }

func (HS300Source) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, "hs300")
	if !stale {
		return freshSkipResult("hs300", "hs300 (Yahoo ^HS300)", lastDate, s), nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}

	var pts []store.PricePoint
	var err error
	if mode == "incremental" {
		pts, err = FetchHS300Incremental(ctx, lastDate)
	} else {
		pts, err = FetchHS300PricePoints(ctx)
	}
	if err != nil {
		return nil, err
	}
	if len(pts) == 0 {
		count, _ := s.Count("hs300")
		return &RefreshResult{
			Key: "hs300", DisplayName: "hs300 (Yahoo ^HS300)",
			Mode: mode, Added: 0, Total: count, LastDate: lastDate,
		}, nil
	}

	if mode == "full" {
		if err := s.Save("hs300", pts); err != nil {
			return nil, err
		}
	} else {
		if err := s.Append("hs300", pts); err != nil {
			return nil, err
		}
	}
	count, _ := s.Count("hs300")
	last, _ := s.LastDate("hs300")
	return &RefreshResult{
		Key: "hs300", DisplayName: "hs300 (Yahoo ^HS300)",
		Mode: mode, Added: len(pts), Total: count, LastDate: last,
	}, nil
}

// StockKeysSource refreshes every previously-imported stock_<ticker> key.
// Iterates s.ListAssets() looking for the StockNamespacePrefix.
type StockKeysSource struct{}

func (StockKeysSource) Key() string         { return "stock_*" }
func (StockKeysSource) DisplayName() string { return "stock_* (every imported ticker)" }

func (StockKeysSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	keys, err := s.ListAssets()
	if err != nil {
		return nil, err
	}
	added := 0
	mode := "skip"
	var lastErr error
	for _, k := range keys {
		if len(k) < len(StockNamespacePrefix) || k[:len(StockNamespacePrefix)] != StockNamespacePrefix {
			continue
		}
		ticker := k[len(StockNamespacePrefix):]
		// FetchAndCacheStock has its own TTL skip.
		pre, _ := s.Count(k)
		_, err := FetchAndCacheStock(ctx, s, ticker, 3650)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", k, err)
			continue
		}
		post, _ := s.Count(k)
		delta := post - pre
		if delta > 0 {
			added += delta
			mode = "incremental"
		}
	}
	res := &RefreshResult{
		Key: "stock_*", DisplayName: "stock_* (every imported ticker)",
		Mode: mode, Added: added, LastDate: time.Now().UTC().Format("2006-01-02"),
	}
	if lastErr != nil {
		res.Error = lastErr.Error()
	}
	return res, nil
}
