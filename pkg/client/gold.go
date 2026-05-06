// gold.go — London Gold (XAU/USD) price pipeline.
//
// Data sources:
//   - DBnomics LBMA PM Fix for deep history (1968+). Free, no API key.
//     Provider: LBMA or World Bank GOLD/PM series.
//   - Yahoo Finance XAUUSD=X for incremental daily updates (recent data).
//     Yahoo fill-gaps when DBnomics is unavailable.
//
// Staleness: LBMA publishes daily PM fix on business days only.
// Yahoo XAUUSD=X covers all trading days.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	goldMaxHistoryDays = 22000 // ~60 years
	goldRecentDays     = 30
)

// dbnomicsSeriesResp DBnomics v22 series/observations response
type dbnomicsSeriesResp struct {
	Docs []struct {
		ProviderName string `json:"provider_name"`
		DatasetName  string `json:"dataset_name"`
		SeriesName   string `json:"series_name"`
		Period       string `json:"period"`
		Value        string `json:"value"`
	} `json:"docs"`
	Total struct {
		Value int `json:"value"`
	} `json:"total"`
}

// FetchLondonGoldPricePoints fetches XAU/USD daily price history.
// Returns oldest-first PricePoint slice.
// Uses DBnomics LBMA for deep history; falls back to Yahoo XAUUSD=X.
func FetchLondonGoldPricePoints(ctx context.Context) ([]store.PricePoint, error) {
	hc := &http.Client{Timeout: 30 * time.Second}

	// Try DBnomics first for deep history
	points, err := fetchDBnomicsGold(ctx, hc)
	if err != nil || len(points) < 1000 {
		// Fall back to Yahoo XAUUSD=X
		yahooPoints, yahooErr := fetchYahooGold(ctx, hc)
		if yahooErr != nil {
			if err != nil {
				return nil, fmt.Errorf("gold fetch failed: dbnomics=%w, yahoo=%w", err, yahooErr)
			}
			return nil, yahooErr
		}
		points = yahooPoints
	}

	// Always overlay recent Yahoo data for freshness
	recent, err := fetchYahooGold(ctx, hc)
	if err == nil && len(recent) > 0 {
		points = mergeGoldPoints(points, recent)
	}

	return store.NormalizePricePoints(points), nil
}

// fetchDBnomicsGold fetches LBMA PM gold fix from DBnomics.
// Known series:
//   - primary: LBMA provider (preferred, covers 1968+)
//   - fallback: WB/GOLD/PM (World Bank commodity prices)
func fetchDBnomicsGold(ctx context.Context, hc *http.Client) ([]store.PricePoint, error) {
	// DBnomics LBMA gold series. Try known provider codes.
	providers := []string{"LBMA", "WB"}
	datasets := []string{"GOLD", "GOLD"}
	series := []string{"PM", "PM"}

	for i := range providers {
		u := fmt.Sprintf(
			"https://api.db.nomics.world/v22/series/%s/%s/%s?observations=1&limit=25000",
			url.PathEscape(providers[i]),
			url.PathEscape(datasets[i]),
			url.PathEscape(series[i]),
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")

		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}

		var parsed dbnomicsSeriesResp
		if err := json.Unmarshal(body, &parsed); err != nil {
			continue
		}
		if len(parsed.Docs) == 0 {
			continue
		}

		points := make([]store.PricePoint, 0, len(parsed.Docs))
		for _, doc := range parsed.Docs {
			if doc.Value == "" || doc.Value == "." {
				continue
			}
			var val float64
			if _, err := fmt.Sscanf(doc.Value, "%f", &val); err != nil || val <= 0 {
				continue
			}
			points = append(points, store.PricePoint{
				Date:   doc.Period,
				Close:  val,
				Source: fmt.Sprintf("dbnomics:%s/%s", providers[i], datasets[i]),
			})
		}
		if len(points) >= 1000 {
			return points, nil
		}
	}

	return nil, fmt.Errorf("no DBnomics gold series returned usable data")
}

// fetchYahooGold fetches XAUUSD=X from Yahoo Finance chart API.
func fetchYahooGold(ctx context.Context, hc *http.Client) ([]store.PricePoint, error) {
	now := time.Now().Unix()
	from := now - int64(goldMaxHistoryDays*86400)

	params := url.Values{}
	params.Set("period1", fmt.Sprintf("%d", from))
	params.Set("period2", fmt.Sprintf("%d", now))
	params.Set("interval", "1d")
	params.Set("includePrePost", "false")

	apiURL := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/XAUUSD=X?%s", params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("yahoo gold http %d: %s", resp.StatusCode, string(body))
	}

	var parsed yahooChartResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Chart.Result) == 0 || len(parsed.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo gold empty result")
	}

	r := parsed.Chart.Result[0]
	closes := r.Indicators.Quote[0].Close
	timestamps := r.Timestamp

	points := make([]store.PricePoint, 0, len(closes))
	for i, c := range closes {
		if c == nil || *c <= 0 {
			continue
		}
		date := ""
		if i < len(timestamps) {
			date = time.Unix(timestamps[i], 0).UTC().Format("2006-01-02")
		}
		points = append(points, store.PricePoint{
			Date:   date,
			Close:  *c,
			Source: "yahoo:XAUUSD=X",
		})
	}

	if len(points) == 0 {
		return nil, fmt.Errorf("yahoo gold: no valid closes")
	}
	return points, nil
}

func mergeGoldPoints(base, updates []store.PricePoint) []store.PricePoint {
	merged := make([]store.PricePoint, 0, len(base)+len(updates))
	merged = append(merged, base...)
	merged = append(merged, updates...)
	return store.NormalizePricePoints(merged)
}

// FetchGoldIncremental fetches only recent gold data for PriceStore updates.
func FetchGoldIncremental(ctx context.Context, lastDate string) ([]store.PricePoint, error) {
	if lastDate == "" {
		return FetchLondonGoldPricePoints(ctx)
	}
	last, err := time.Parse("2006-01-02", lastDate)
	if err != nil {
		return FetchLondonGoldPricePoints(ctx)
	}

	daysSince := int(time.Since(last).Hours() / 24)
	if daysSince <= 1 {
		return nil, nil // fresh enough
	}

	return FetchLondonGoldPricePoints(ctx)
}

// FetchFearGreedHistory fetches the full Fear & Greed index history from alternative.me.
// The API supports ?limit=0 to get all historical data (2018-02-01 onwards).
func FetchFearGreedHistory(ctx context.Context) ([]store.PricePoint, error) {
	hc := &http.Client{Timeout: 20 * time.Second}
	u := "https://api.alternative.me/fng/?limit=0"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("fear & greed http %d: %s", resp.StatusCode, string(body))
	}

	type fngResp struct {
		Data []struct {
			Value     string `json:"value"`
			Timestamp string `json:"timestamp"`
		} `json:"data"`
	}

	var parsed fngResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("fear & greed: empty response")
	}

	points := make([]store.PricePoint, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		var val float64
		if _, err := fmt.Sscanf(d.Value, "%f", &val); err != nil || val < 0 || val > 100 {
			continue
		}
		ts, err := parseInt(d.Timestamp)
		if err != nil {
			continue
		}
		date := time.Unix(ts, 0).UTC().Format("2006-01-02")
		points = append(points, store.PricePoint{
			Date:   date,
			Close:  val,
			Source: "alternative.me:fng",
		})
	}

	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })
	return points, nil
}

func parseInt(s string) (int64, error) {
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, err
	}
	return v, nil
}
