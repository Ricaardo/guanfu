// Legacy Stooq Put/Call ratio source.
//
// The default refresh path now uses CBOE official no-key data via
// CBOEPutCallSource while keeping the same storage key (`stooq_putcall`) for
// compatibility. This source remains as a manual fallback for users who have a
// Stooq CSV key.

package client

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	stooqPutCallURL = "https://stooq.com/q/d/l/?s=^pc&i=d"
	stooqTimeout    = 60 * time.Second
	stooqAPIKeyEnv  = "STOOQ_APIKEY"
)

// StooqPutCallSource fetches CBOE total Put/Call ratio daily history from
// Stooq. It is not part of the default refresh source list; prefer
// CBOEPutCallSource for no-key official CBOE data.
type StooqPutCallSource struct{}

func (StooqPutCallSource) Key() string         { return "stooq_putcall" }
func (StooqPutCallSource) DisplayName() string { return "stooq_putcall (legacy Stooq fallback)" }

// Refresh fetches the full Stooq CSV when store is empty; appends otherwise.
// Stooq returns the full history in one call (~5000 rows), so the cost
// difference between full and incremental is negligible — we re-fetch
// and dedupe via Save/Append.
func (s StooqPutCallSource) Refresh(ctx context.Context, ps *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(ps, s.Key())
	if !stale {
		return freshSkipResult(s.Key(), s.DisplayName(), lastDate, ps), nil
	}

	apiKey := strings.TrimSpace(os.Getenv(stooqAPIKeyEnv))
	if apiKey == "" {
		count, _ := ps.Count(s.Key())
		return &RefreshResult{
			Key:         s.Key(),
			DisplayName: s.DisplayName(),
			Mode:        "skip",
			SkipReason:  "config",
			Stale:       stale,
			Action:      "configure",
			Total:       count,
			LastDate:    lastDate,
			Error:       stooqAPIKeyEnv + " not set; Stooq now requires an API key for CSV downloads",
		}, nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}
	points, err := fetchStooqCSV(ctx, stooqURLWithAPIKey(stooqPutCallURL, apiKey), "stooq:^PC")
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return &RefreshResult{
			Key: s.Key(), DisplayName: s.DisplayName(),
			Mode: mode, SkipReason: "no_new_data", Added: 0, LastDate: lastDate,
		}, nil
	}

	var added int
	if lastDate == "" {
		if err := ps.Save(s.Key(), points); err != nil {
			return nil, err
		}
		added = len(points)
	} else {
		before, _ := ps.Count(s.Key())
		if err := ps.Append(s.Key(), points); err != nil {
			return nil, err
		}
		after, _ := ps.Count(s.Key())
		added = after - before
		mode = "incremental"
	}

	total, _ := ps.Count(s.Key())
	last, _ := ps.LastDate(s.Key())
	return &RefreshResult{
		Key: s.Key(), DisplayName: s.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

func stooqURLWithAPIKey(raw, apiKey string) string {
	if apiKey == "" {
		return raw
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "apikey=" + url.QueryEscape(apiKey)
}

// fetchStooqCSV pulls a Stooq CSV (header: Date,Open,High,Low,Close,Volume)
// and returns closes as PricePoints. sourceTag goes into PricePoint.Source.
// Kept generic so future Stooq sources (NAAIM, AAII alternates) can reuse.
func fetchStooqCSV(ctx context.Context, url, sourceTag string) ([]store.PricePoint, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "guanfu/1.0 (F6 Stooq)")

	c := &http.Client{Timeout: stooqTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stooq fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("stooq %s %d: %s", url, resp.StatusCode, truncate(string(body), 200))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Stooq sometimes returns "No data" (plain text, 200 OK) when a symbol
	// has been renamed or delisted — detect by leading "No data" marker.
	if strings.HasPrefix(strings.TrimSpace(string(body)), "No data") {
		return nil, fmt.Errorf("stooq reports 'No data' for %s", url)
	}
	r := csv.NewReader(strings.NewReader(string(body)))
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("stooq csv parse: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("stooq csv empty: %d rows", len(rows))
	}

	// Header: Date,Open,High,Low,Close,Volume. Find column indices so a
	// future column reorder doesn't silently break things.
	header := rows[0]
	dateCol, closeCol := -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "date":
			dateCol = i
		case "close":
			closeCol = i
		}
	}
	if dateCol < 0 || closeCol < 0 {
		return nil, fmt.Errorf("stooq csv header missing Date/Close: %v", header)
	}

	out := make([]store.PricePoint, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) <= closeCol {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[closeCol]), 64)
		if err != nil || v <= 0 {
			continue
		}
		date := strings.TrimSpace(row[dateCol])
		// Stooq dates are already YYYY-MM-DD.
		if _, err := time.Parse("2006-01-02", date); err != nil {
			continue
		}
		out = append(out, store.PricePoint{
			Date:   date,
			Close:  v,
			Source: sourceTag,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}
