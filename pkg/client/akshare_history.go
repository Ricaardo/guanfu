// AkShare bridge wrapper for HS300 macros (PMI / M2 / LPR / northbound / volume / CPI).
//
// The Python bridge at bin/akshare_bridge.py exposes a `hs300_macro` mode
// returning oldest-first {date, close} points. This file shells out and
// translates them into PriceStore writes with incremental Append semantics.
//
// CNY/USD is intentionally excluded — akshare's USD-CNY endpoints are
// unreliable; YahooETFSource handles that under storage key hs300_cny via
// symbol "CNY=X".

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ricaardo/guanfu/pkg/store"
)

// AkshareSource refreshes one akshare-derived series.
type AkshareSource struct {
	StoreKey string // e.g. "hs300_pmi"
	Series   string // bridge series name, e.g. "pmi"
	Note     string // human description
	Source   string // value persisted in PricePoint.Source, e.g. "akshare:pmi"
}

// DefaultAkshareSources covers everything written into hs300_* by the
// historical import job (minus the Yahoo-handled CNY series).
func DefaultAkshareSources() []*AkshareSource {
	return []*AkshareSource{
		{StoreKey: "hs300_pmi", Series: "pmi", Note: "China official mfg PMI (monthly)", Source: "akshare:pmi"},
		{StoreKey: "hs300_m2", Series: "m2", Note: "China M2 YoY (monthly)", Source: "akshare:m2_yoy"},
		{StoreKey: "hs300_lpr", Series: "lpr", Note: "China LPR 1Y (daily)", Source: "akshare:lpr1y"},
		{StoreKey: "hs300_volume", Series: "volume", Note: "CSI300 daily volume", Source: "akshare:csi300_volume"},
		{StoreKey: "hs300_northbound", Series: "northbound", Note: "Northbound net buy (daily)", Source: "akshare:northbound"},
		{StoreKey: "hs300_cpi", Series: "cpi", Note: "China CPI YoY (monthly)", Source: "akshare:CPI"},
	}
}

func (a *AkshareSource) Key() string         { return a.StoreKey }
func (a *AkshareSource) DisplayName() string { return a.StoreKey + " (" + a.Note + ")" }

// Refresh runs the akshare bridge and merges new points into PriceStore.
// The bridge always returns the full series; we then Append (which dedups by
// date inside PriceStore.Save), so the storage cost is independent of run
// frequency. Skips entirely when the store is fresh (≤1d old).
func (a *AkshareSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, a.StoreKey)
	if !stale {
		return freshSkipResult(a.StoreKey, a.DisplayName(), lastDate, s), nil
	}

	bridge, err := findAkshareBridgePath()
	if err != nil {
		return &RefreshResult{
			Key: a.StoreKey, DisplayName: a.DisplayName(),
			Mode: "skip", LastDate: lastDate, Error: err.Error(),
		}, nil
	}

	payload := fmt.Sprintf(`{"mode":"hs300_macro","series":%q}`, a.Series)
	cmd := exec.CommandContext(ctx, "python3", bridge)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("akshare bridge %s: %w", a.Series, err)
	}

	var resp map[string]struct {
		Points []struct {
			Date  string  `json:"date"`
			Close float64 `json:"close"`
		} `json:"points"`
		Count int    `json:"count"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("akshare bridge %s decode: %w", a.Series, err)
	}
	r, ok := resp[a.Series]
	if !ok {
		return nil, fmt.Errorf("akshare bridge %s: missing key in response", a.Series)
	}
	if r.Error != "" {
		return nil, fmt.Errorf("akshare bridge %s: %s", a.Series, r.Error)
	}
	if len(r.Points) == 0 {
		count, _ := s.Count(a.StoreKey)
		return &RefreshResult{
			Key: a.StoreKey, DisplayName: a.DisplayName(),
			Mode: "incremental", Added: 0, Total: count, LastDate: lastDate,
		}, nil
	}

	// Convert + tag source.
	pts := make([]store.PricePoint, len(r.Points))
	for i, p := range r.Points {
		pts[i] = store.PricePoint{Date: p.Date, Close: p.Close, Source: a.Source}
	}

	// Bridge returns full series; Append + dedup-on-date handles incremental
	// merge cleanly without us having to slice to the gap.
	pre, _ := s.Count(a.StoreKey)
	mode := "full"
	if pre > 0 {
		mode = "incremental"
	}
	if err := s.Append(a.StoreKey, pts); err != nil {
		return nil, err
	}
	post, _ := s.Count(a.StoreKey)
	last, _ := s.LastDate(a.StoreKey)
	return &RefreshResult{
		Key: a.StoreKey, DisplayName: a.DisplayName(),
		Mode: mode, Added: post - pre, Total: post, LastDate: last,
	}, nil
}

// findAkshareBridgePath looks in well-known locations.
// Tracked in scripts/; legacy bin/ path kept as fallback for older checkouts.
func findAkshareBridgePath() (string, error) {
	candidates := []string{
		"scripts/akshare_bridge.py",
		filepath.Join("..", "scripts", "akshare_bridge.py"),
		"bin/akshare_bridge.py",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "guanfu", "scripts", "akshare_bridge.py"),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("akshare_bridge.py not found in any of %v", candidates)
}
