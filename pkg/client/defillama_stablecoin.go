// DefiLlama stablecoin net-flow source (F2).
//
// Why this source:
//   PriceStore already has `stablecoin_market_cap_usd` — a single snapshot
//   of total supply. DefiLlama's /stablecoincharts/all endpoint gives the
//   per-day aggregate USD supply time series, free, no key. From that we
//   derive what extractors actually need: the 30-day change (net inflow
//   proxy), which is "fresh crypto-native dollars landing on chain."
//
// Storage key: `defillama_stablecoin_supply` (single daily point stream
// of aggregate stablecoin market cap in USD). Derived features (30d pct
// change, 7d roll) live in forecast extractors; we keep the raw series here
// so callers can compute their own windows.
//
// Rate limits: DefiLlama is permissive for unauthenticated GET; no key.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	defillamaStablecoinChartURL = "https://stablecoins.llama.fi/stablecoincharts/all"
	defillamaTimeout            = 45 * time.Second
)

// DefillamaStablecoinSource pulls the aggregate stablecoin supply (USD)
// daily history from DefiLlama. Key: defillama_stablecoin_supply.
type DefillamaStablecoinSource struct{}

func (DefillamaStablecoinSource) Key() string         { return "defillama_stablecoin_supply" }
func (DefillamaStablecoinSource) DisplayName() string { return "defillama_stablecoin_supply (F2)" }

// Refresh fetches the full DefiLlama chart when the store is empty;
// otherwise just appends any new points. The endpoint returns the entire
// series in one call (~1000 rows as of 2026), so incremental is really
// "re-fetch then dedupe via Save/Append" — cheap enough on this cadence.
func (d DefillamaStablecoinSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, d.Key())
	if !stale {
		return freshSkipResult(d.Key(), d.DisplayName(), lastDate, s), nil
	}

	points, err := fetchDefillamaStablecoin(ctx)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return &RefreshResult{
			Key: d.Key(), DisplayName: d.DisplayName(),
			Mode: "full", Added: 0, LastDate: lastDate,
		}, nil
	}

	mode := "full"
	var added int
	if lastDate == "" {
		if err := s.Save(d.Key(), points); err != nil {
			return nil, err
		}
		added = len(points)
	} else {
		// Append new ones only; Save dedupes by date.
		before, _ := s.Count(d.Key())
		if err := s.Append(d.Key(), points); err != nil {
			return nil, err
		}
		after, _ := s.Count(d.Key())
		added = after - before
		mode = "incremental"
	}

	total, _ := s.Count(d.Key())
	last, _ := s.LastDate(d.Key())
	return &RefreshResult{
		Key: d.Key(), DisplayName: d.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

// fetchDefillamaStablecoin returns the all-stablecoins aggregate time
// series, oldest-first, date-normalized. DefiLlama's schema at this
// endpoint: [{"date":"unix_sec","totalCirculatingUSD":{"peggedUSD":N,...}}, ...]
func fetchDefillamaStablecoin(ctx context.Context) ([]store.PricePoint, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", defillamaStablecoinChartURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "guanfu/1.0 (F2 DefiLlama stablecoins)")
	req.Header.Set("Accept", "application/json")

	c := &http.Client{Timeout: defillamaTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("defillama stablecoin fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("defillama stablecoin %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Date                any            `json:"date"`
		TotalCirculatingUSD map[string]any `json:"totalCirculatingUSD"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("defillama stablecoin parse: %w", err)
	}

	out := make([]store.PricePoint, 0, len(raw))
	for _, r := range raw {
		ts, ok := parseDefillamaDate(r.Date)
		if !ok {
			continue
		}
		total := sumPeggedValues(r.TotalCirculatingUSD)
		if total <= 0 {
			continue
		}
		out = append(out, store.PricePoint{
			Date:   ts.UTC().Format("2006-01-02"),
			Close:  total,
			Source: "defillama:stablecoin_supply",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// parseDefillamaDate handles both numeric unix-seconds and string date
// forms DefiLlama has historically emitted.
func parseDefillamaDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case float64:
		return time.Unix(int64(x), 0).UTC(), true
	case int64:
		return time.Unix(x, 0).UTC(), true
	case string:
		s := strings.TrimSpace(x)
		// try unix seconds as string
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.UTC(), true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// sumPeggedValues totals every peggedUSD/peggedEUR/... in the
// totalCirculatingUSD map. DefiLlama converts non-USD stablecoin market
// caps to USD before reporting, so a straight sum is the right aggregate.
func sumPeggedValues(m map[string]any) float64 {
	total := 0.0
	for _, v := range m {
		f, ok := v.(float64)
		if !ok {
			continue
		}
		total += f
	}
	return total
}
