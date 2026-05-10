// Unified data refresh framework.
//
// Each Source represents a PriceStore key (or related group) and knows how
// to do a full historical pull on first run, plus an incremental update on
// subsequent runs. RefreshAll runs sources in dependency order and reports
// which were skipped (already fresh), updated, or failed.
//
// The contract for a Source.Refresh:
//   1. Inspect PriceStore for last_date.
//   2. If fresh (≤1d ago), return Mode="skip".
//   3. If empty / very old, do full historical pull and Save.
//   4. If gap exists, fetch only the gap and Append.
//   5. Return RefreshResult with mode, count added, last_date, error.

package client

import (
	"context"
	"fmt"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

// Source describes a single dataset that can be refreshed.
type Source interface {
	Key() string
	DisplayName() string
	Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error)
}

// RefreshResult summarizes what happened for one source.
type RefreshResult struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Mode        string `json:"mode"` // "skip" | "full" | "incremental" | "fail"
	Added       int    `json:"added"`
	Total       int    `json:"total"`
	LastDate    string `json:"last_date"`
	Duration    string `json:"duration"`
	Error       string `json:"error,omitempty"`
}

// RefreshAll runs every source sequentially and returns per-source results.
// Sequential is intentional — most sources hit external APIs with their own
// rate limits, and serial output is easier to debug than interleaved.
// A failing source does not stop the loop; its error is captured in the
// result and the next source runs.
func RefreshAll(ctx context.Context, s *store.PriceStore, sources []Source) []*RefreshResult {
	out := make([]*RefreshResult, 0, len(sources))
	for _, src := range sources {
		start := time.Now()
		r, err := src.Refresh(ctx, s)
		if r == nil {
			r = &RefreshResult{Key: src.Key(), DisplayName: src.DisplayName()}
		}
		if err != nil {
			r.Mode = "fail"
			r.Error = err.Error()
		}
		r.Duration = time.Since(start).Round(time.Millisecond).String()
		out = append(out, r)
	}
	return out
}

// staleThreshold returns true if the asset's last_date is older than 24h.
// "Fresh" means we skip the network call entirely.
func staleThreshold(s *store.PriceStore, key string) (stale bool, lastDate string) {
	last, err := s.LastDate(key)
	if err != nil || last == "" {
		return true, ""
	}
	t, err := time.Parse("2006-01-02", last)
	if err != nil {
		return true, last
	}
	return time.Since(t) > 24*time.Hour, last
}

// freshSkipResult builds a no-op result for a source whose data is recent.
func freshSkipResult(key, name, lastDate string, s *store.PriceStore) *RefreshResult {
	count, _ := s.Count(key)
	return &RefreshResult{
		Key:         key,
		DisplayName: name,
		Mode:        "skip",
		Total:       count,
		LastDate:    lastDate,
	}
}

// FormatRefreshTable renders results as a human-readable status table.
func FormatRefreshTable(results []*RefreshResult) string {
	if len(results) == 0 {
		return "no refresh sources configured"
	}
	out := fmt.Sprintf("%-22s %-12s %8s %12s %10s\n", "DATASET", "MODE", "ADDED", "TOTAL", "LAST_DATE")
	out += fmt.Sprintf("%-22s %-12s %8s %12s %10s\n", "-------", "----", "-----", "-----", "---------")
	for _, r := range results {
		mode := r.Mode
		if r.Error != "" {
			if r.Mode == "skip" {
				mode = "SKIP: " + truncate(r.Error, 40)
			} else {
				mode = "FAIL: " + truncate(r.Error, 40)
			}
		}
		out += fmt.Sprintf("%-22s %-12s %8d %12d %10s\n",
			truncate(r.Key, 22), mode, r.Added, r.Total, r.LastDate)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
