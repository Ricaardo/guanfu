// Coinbase BTC-USD daily close source (F8).
//
// Coinbase spot price alone isn't particularly interesting; what matters
// is the *premium over Binance BTC-USDT*, used as a proxy for US
// institutional demand vs. global retail. We persist just the Coinbase
// side here; the differential is computed at display/extractor time
// against the existing `btc` key (CoinMetrics + Binance blend).
//
// Coinbase has a free public candles endpoint (no key, no auth):
//
//   GET https://api.exchange.coinbase.com/products/BTC-USD/candles?granularity=86400
//
// Returns up to 300 candles per call, oldest-first. For a full history
// we chunk by 300-day windows back from today. The API is stable — it's
// survived unchanged since 2019 — so hardcoding the endpoint is safe.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	coinbaseCandlesURL = "https://api.exchange.coinbase.com/products/BTC-USD/candles"
	coinbaseTimeout    = 45 * time.Second
	coinbasePageDays   = 300 // Coinbase max per request
	coinbaseFullYears  = 6   // how far back we go on initial import
)

// CoinbaseBTCSource pulls BTC-USD daily close from Coinbase for the
// premium-vs-Binance signal. Key: coinbase_btc.
type CoinbaseBTCSource struct{}

func (CoinbaseBTCSource) Key() string { return "coinbase_btc" }
func (CoinbaseBTCSource) DisplayName() string {
	return "coinbase_btc (BTC-USD daily, F8 premium proxy)"
}

func (c CoinbaseBTCSource) Refresh(ctx context.Context, ps *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(ps, c.Key())
	if !stale {
		return freshSkipResult(c.Key(), c.DisplayName(), lastDate, ps), nil
	}

	// Determine fetch window. For incremental: since lastDate + 1 day.
	// For full: coinbaseFullYears ago.
	now := time.Now().UTC()
	var start time.Time
	mode := "full"
	if lastDate != "" {
		if t, err := time.Parse("2006-01-02", lastDate); err == nil {
			start = t.AddDate(0, 0, 1)
			mode = "incremental"
		}
	}
	if start.IsZero() {
		start = now.AddDate(-coinbaseFullYears, 0, 0)
	}

	points, err := fetchCoinbaseBTCDaily(ctx, start, now)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return &RefreshResult{
			Key: c.Key(), DisplayName: c.DisplayName(),
			Mode: mode, Added: 0, LastDate: lastDate,
		}, nil
	}

	var added int
	if lastDate == "" {
		if err := ps.Save(c.Key(), points); err != nil {
			return nil, err
		}
		added = len(points)
	} else {
		before, _ := ps.Count(c.Key())
		if err := ps.Append(c.Key(), points); err != nil {
			return nil, err
		}
		after, _ := ps.Count(c.Key())
		added = after - before
	}

	total, _ := ps.Count(c.Key())
	last, _ := ps.LastDate(c.Key())
	return &RefreshResult{
		Key: c.Key(), DisplayName: c.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

// fetchCoinbaseBTCDaily chunks the [start, end] range into 300-day
// pages (Coinbase's per-request cap) and returns a merged sorted slice.
func fetchCoinbaseBTCDaily(ctx context.Context, start, end time.Time) ([]store.PricePoint, error) {
	if !end.After(start) {
		return nil, nil
	}
	var all []store.PricePoint
	cur := start
	for cur.Before(end) {
		windowEnd := cur.AddDate(0, 0, coinbasePageDays-1)
		if windowEnd.After(end) {
			windowEnd = end
		}
		pts, err := fetchCoinbaseWindow(ctx, cur, windowEnd)
		if err != nil {
			// One window failure shouldn't lose the rest; propagate up so
			// RefreshAll marks it fail — but the partial `all` is discarded
			// (safer than half-saving a gap).
			return nil, err
		}
		all = append(all, pts...)
		cur = windowEnd.AddDate(0, 0, 1)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Date < all[j].Date })
	// Dedupe by date (windows can overlap by 1 on boundaries).
	out := make([]store.PricePoint, 0, len(all))
	seen := map[string]bool{}
	for _, p := range all {
		if seen[p.Date] {
			continue
		}
		seen[p.Date] = true
		out = append(out, p)
	}
	return out, nil
}

func fetchCoinbaseWindow(ctx context.Context, start, end time.Time) ([]store.PricePoint, error) {
	url := fmt.Sprintf("%s?granularity=86400&start=%s&end=%s",
		coinbaseCandlesURL,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "guanfu/1.0 (F8 Coinbase BTC)")
	req.Header.Set("Accept", "application/json")

	cli := &http.Client{Timeout: coinbaseTimeout}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinbase fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("coinbase %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	// Response shape: [[time, low, high, open, close, volume], ...]
	var raw [][]float64
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("coinbase decode: %w", err)
	}
	out := make([]store.PricePoint, 0, len(raw))
	for _, row := range raw {
		if len(row) < 5 {
			continue
		}
		ts := time.Unix(int64(row[0]), 0).UTC()
		close := row[4]
		if close <= 0 {
			continue
		}
		out = append(out, store.PricePoint{
			Date:   ts.Format("2006-01-02"),
			Close:  close,
			Source: "coinbase:BTC-USD",
		})
	}
	return out, nil
}
