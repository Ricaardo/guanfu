// Shared Yahoo Finance chart API response decoding.
// Uses yahooChartResp defined in cross_asset.go.

package client

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

func decodeYahooResponse(r io.Reader) (*yahooChartResp, error) {
	var parsed yahooChartResp
	if err := json.NewDecoder(r).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Chart.Result) == 0 || len(parsed.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo: empty result")
	}
	return &parsed, nil
}

func yahooRespToPricePoints(parsed *yahooChartResp, symbol, source string) []store.PricePoint {
	result := parsed.Chart.Result[0]
	closes := result.Indicators.Quote[0].Close
	timestamps := result.Timestamp

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
			Source: source,
		})
	}
	return points
}

func yahooRespToFloat64(parsed *yahooChartResp) (price float64, history []float64, asOf string) {
	result := parsed.Chart.Result[0]
	closes := result.Indicators.Quote[0].Close
	timestamps := result.Timestamp

	history = make([]float64, 0, len(closes))
	for _, c := range closes {
		if c != nil && *c > 0 {
			history = append(history, *c)
		}
	}
	if len(history) == 0 {
		return 0, nil, ""
	}

	// Meta price
	if meta := result.Meta; meta.RegularMarketPrice > 0 {
		price = meta.RegularMarketPrice
		if len(timestamps) > 0 {
			asOf = time.Unix(timestamps[len(timestamps)-1], 0).UTC().Format("2006-01-02")
		}
	}
	if price == 0 {
		price = history[len(history)-1]
	}
	return
}
