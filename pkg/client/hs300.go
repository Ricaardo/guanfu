// CSI300 (沪深300) data pipeline.
//
// Data source: Yahoo Finance 000300.SS (daily, free, no API key).
// Historical depth: ~2015+ (Yahoo provides ~10 years).
// Incremental: PriceStore checks last_date and fetches only new data.

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const hs300MaxDays = 4000

// FetchHS300PricePoints fetches CSI300 daily close prices from Yahoo Finance.
// Returns oldest-first PricePoint slice.
func FetchHS300PricePoints(ctx context.Context) ([]store.PricePoint, error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	return fetchHS300Yahoo(ctx, hc)
}

// FetchHS300Incremental fetches only new data since last_date.
func FetchHS300Incremental(ctx context.Context, lastDate string) ([]store.PricePoint, error) {
	if lastDate == "" {
		return FetchHS300PricePoints(ctx)
	}
	last, err := time.Parse("2006-01-02", lastDate)
	if err != nil {
		return FetchHS300PricePoints(ctx)
	}
	if time.Since(last).Hours()/24 <= 1 {
		return nil, nil
	}
	return FetchHS300PricePoints(ctx)
}

func fetchHS300Yahoo(ctx context.Context, hc *http.Client) ([]store.PricePoint, error) {
	now := time.Now().Unix()
	from := now - int64(hs300MaxDays*86400)

	params := url.Values{}
	params.Set("period1", fmt.Sprintf("%d", from))
	params.Set("period2", fmt.Sprintf("%d", now))
	params.Set("interval", "1d")
	params.Set("includePrePost", "false")

	apiURL := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/000300.SS?%s", params.Encode())
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
		return nil, fmt.Errorf("yahoo 000300.SS http %d: %s", resp.StatusCode, string(body))
	}

	parsed, err := decodeYahooResponse(resp.Body)
	if err != nil {
		return nil, err
	}
	return yahooRespToPricePoints(parsed, "000300.SS", "yahoo:000300.SS"), nil
}

// ImportHS300ToPriceStore fetches HS300 data and stores it in PriceStore.
func ImportHS300ToPriceStore(ctx context.Context) error {
	points, err := FetchHS300PricePoints(ctx)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return fmt.Errorf("hs300: yahoo returned empty data")
	}
	s := &store.PriceStore{}
	return s.Save("hs300", points)
}
