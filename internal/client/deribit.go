// Deribit BTC options client — DVOL volatility index + 25-delta skew.
//
// Public endpoints (no auth, no rate limit issues for daily polling):
//   - DVOL OHLC: /api/v2/public/get_volatility_index_data?currency=BTC
//   - Instruments: /api/v2/public/get_instruments?currency=BTC&kind=option
//   - Ticker: /api/v2/public/ticker?instrument_name=...
//
// DVOL = Deribit's BTC IV index, analog of equity VIX for BTC. Higher DVOL = market
// expects bigger moves. Forward-looking signal: typically falls before bottoms,
// rises before tops.
//
// 25-delta skew = IV(25Δ put) - IV(25Δ call). Positive skew (put more expensive)
// = downside hedging demand, fear. Negative skew = upside chase, greed/euphoria.
//
// All values returned are guaranteed finite. Failures return Available=false data
// so the caller can decide to skip.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"
)

// DeribitOptionsData — DVOL + skew. Available bool tells caller whether to use it.
type DeribitOptionsData struct {
	DVOL              float64 // current DVOL value
	DVOL60dTrendPct   float64 // (now / 60d-ago - 1) * 100
	DVOLAvailable     bool
	DVOLAsOf          time.Time
	DVOLHistory       []float64 // up to ~365 daily samples (resolution=86400)

	Skew25dNearTermPct float64 // IV(25Δ put) - IV(25Δ call) for nearest-monthly expiry, percentage points
	SkewAvailable      bool
	SkewAsOf           time.Time
	SkewExpiry         string // expiry instrument name used

	Warnings []string
}

const (
	deribitBase       = "https://www.deribit.com/api/v2/public"
	deribitDVOLLookback = 365 // 1 year of daily DVOL
	deribitTimeout    = 10 * time.Second
)

// FetchBTCDeribitOptions — best-effort. Never returns an error; failures show up
// as Available=false on individual sub-fields so the caller can keep going.
func FetchBTCDeribitOptions(ctx context.Context) *DeribitOptionsData {
	out := &DeribitOptionsData{}
	hc := &http.Client{Timeout: deribitTimeout}

	if dvol, hist, asOf, err := fetchDVOLDaily(ctx, hc, deribitDVOLLookback); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("deribit DVOL fetch failed: %v", err))
	} else {
		out.DVOL = dvol
		out.DVOLHistory = hist
		out.DVOLAsOf = asOf
		out.DVOLAvailable = true
		// 60d trend
		if len(hist) >= 61 {
			past := hist[len(hist)-61]
			if past > 0 {
				out.DVOL60dTrendPct = (dvol/past - 1) * 100
			}
		}
	}

	if skew, expiry, asOf, err := fetch25DSkewNearTerm(ctx, hc); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("deribit skew fetch failed: %v", err))
	} else {
		out.Skew25dNearTermPct = skew
		out.SkewExpiry = expiry
		out.SkewAsOf = asOf
		out.SkewAvailable = true
	}

	return out
}

// --- DVOL ---

type dvolResp struct {
	JSONRPC string `json:"jsonrpc"`
	Result  struct {
		Data       [][]float64 `json:"data"` // [ts_ms, open, high, low, close]
		Continuation interface{} `json:"continuation"`
	} `json:"result"`
}

func fetchDVOLDaily(ctx context.Context, hc *http.Client, lookbackDays int) (current float64, hist []float64, asOf time.Time, err error) {
	end := time.Now().UnixMilli()
	start := time.Now().Add(-time.Duration(lookbackDays+5) * 24 * time.Hour).UnixMilli()
	url := fmt.Sprintf("%s/get_volatility_index_data?currency=BTC&start_timestamp=%d&end_timestamp=%d&resolution=86400",
		deribitBase, start, end)

	body, err := getBody(ctx, hc, url)
	if err != nil {
		return 0, nil, time.Time{}, err
	}
	var parsed dvolResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, nil, time.Time{}, fmt.Errorf("deribit DVOL parse: %w", err)
	}
	if len(parsed.Result.Data) == 0 {
		return 0, nil, time.Time{}, fmt.Errorf("deribit DVOL returned empty data")
	}
	// Each row: [ts_ms, open, high, low, close]. Take close.
	closes := make([]float64, 0, len(parsed.Result.Data))
	var lastTs int64
	for _, row := range parsed.Result.Data {
		if len(row) < 5 {
			continue
		}
		c := row[4]
		if math.IsNaN(c) || math.IsInf(c, 0) || c <= 0 {
			continue
		}
		closes = append(closes, c)
		lastTs = int64(row[0])
	}
	if len(closes) == 0 {
		return 0, nil, time.Time{}, fmt.Errorf("deribit DVOL all rows invalid")
	}
	return closes[len(closes)-1], closes, time.UnixMilli(lastTs).UTC(), nil
}

// --- 25-delta skew ---

type instrument struct {
	Name              string  `json:"instrument_name"`
	Strike            float64 `json:"strike"`
	OptionType        string  `json:"option_type"` // "call" / "put"
	ExpirationTS      int64   `json:"expiration_timestamp"`
}

type instrumentsResp struct {
	Result []instrument `json:"result"`
}

type tickerResp struct {
	Result struct {
		MarkIV    float64 `json:"mark_iv"`
		MarkPrice float64 `json:"mark_price"`
		Greeks    struct {
			Delta float64 `json:"delta"`
		} `json:"greeks"`
		IndexPrice float64 `json:"index_price"`
	} `json:"result"`
}

// fetch25DSkewNearTerm picks the nearest weekly/monthly expiry that is at least
// 7 days out (avoid noisy ultra-short term), finds 25Δ put and 25Δ call by
// scanning all options of that expiry, and returns IV(put) - IV(call) in pp.
func fetch25DSkewNearTerm(ctx context.Context, hc *http.Client) (skew float64, expiry string, asOf time.Time, err error) {
	url := fmt.Sprintf("%s/get_instruments?currency=BTC&kind=option&expired=false", deribitBase)
	body, err := getBody(ctx, hc, url)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	var parsed instrumentsResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, "", time.Time{}, fmt.Errorf("deribit instruments parse: %w", err)
	}
	if len(parsed.Result) == 0 {
		return 0, "", time.Time{}, fmt.Errorf("deribit returned no instruments")
	}

	// Group by expiry, pick smallest expiry that is at least 7 days out.
	now := time.Now().UnixMilli()
	minOffsetMs := int64(7 * 24 * 3600 * 1000)
	expiries := map[int64][]instrument{}
	for _, ins := range parsed.Result {
		if ins.ExpirationTS < now+minOffsetMs {
			continue
		}
		expiries[ins.ExpirationTS] = append(expiries[ins.ExpirationTS], ins)
	}
	if len(expiries) == 0 {
		return 0, "", time.Time{}, fmt.Errorf("no expiry >= 7d out")
	}
	tsList := make([]int64, 0, len(expiries))
	for k := range expiries {
		tsList = append(tsList, k)
	}
	sort.Slice(tsList, func(i, j int) bool { return tsList[i] < tsList[j] })
	chosenTS := tsList[0]
	chosenExpiry := time.UnixMilli(chosenTS).UTC().Format("2006-01-02")
	candidates := expiries[chosenTS]

	// We don't have per-instrument deltas in the instruments endpoint; we need
	// to call /ticker for each one. To keep request count bounded, sample a
	// strike grid: take strikes within ±30% of estimated ATM (use median strike
	// as a proxy). This bounds tickers to ~30-40 calls.
	strikes := make([]float64, 0, len(candidates))
	for _, c := range candidates {
		strikes = append(strikes, c.Strike)
	}
	sort.Float64s(strikes)
	mid := strikes[len(strikes)/2]
	low := mid * 0.7
	high := mid * 1.3

	var calls, puts []quotedOption
	for _, c := range candidates {
		if c.Strike < low || c.Strike > high {
			continue
		}
		t, err := fetchTicker(ctx, hc, c.Name)
		if err != nil || math.IsNaN(t.iv) || t.iv <= 0 {
			continue
		}
		if c.OptionType == "call" {
			calls = append(calls, quotedOption{c, t.iv, t.delta})
		} else if c.OptionType == "put" {
			puts = append(puts, quotedOption{c, t.iv, t.delta})
		}
	}
	if len(calls) == 0 || len(puts) == 0 {
		return 0, "", time.Time{}, fmt.Errorf("not enough quoted options at expiry %s (calls=%d puts=%d)", chosenExpiry, len(calls), len(puts))
	}

	// 25Δ call = delta closest to 0.25; 25Δ put = delta closest to -0.25.
	callIV := closestDelta(calls, 0.25)
	putIV := closestDelta(puts, -0.25)
	skewPct := putIV - callIV
	return skewPct, chosenExpiry, time.Now().UTC(), nil
}

type fetched struct {
	iv    float64
	delta float64
}

func fetchTicker(ctx context.Context, hc *http.Client, name string) (fetched, error) {
	url := fmt.Sprintf("%s/ticker?instrument_name=%s", deribitBase, name)
	body, err := getBody(ctx, hc, url)
	if err != nil {
		return fetched{}, err
	}
	var t tickerResp
	if err := json.Unmarshal(body, &t); err != nil {
		return fetched{}, err
	}
	return fetched{iv: t.Result.MarkIV, delta: t.Result.Greeks.Delta}, nil
}

type quotedOption struct {
	ins instrument
	iv  float64
	dlt float64
}

func closestDelta(set []quotedOption, target float64) float64 {
	bestIV := set[0].iv
	bestDist := math.Abs(set[0].dlt - target)
	for _, q := range set[1:] {
		d := math.Abs(q.dlt - target)
		if d < bestDist {
			bestDist = d
			bestIV = q.iv
		}
	}
	return bestIV
}

func getBody(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("deribit http %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}
