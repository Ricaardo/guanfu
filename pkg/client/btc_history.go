package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/pkg/cache"
	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

const (
	BTCFullHistoryStart = "2010-07-18"

	btcDailyHistoryCacheSchema = 1
	btcDailyHistoryCacheFile   = "btc_daily_history.json"
	btcRecentRefreshDays       = 30
	btcHistoryFreshSlackDays   = 3
	coinMetricsPriceLookback   = 10000
)

// BTCDailyPoint is an oldest-first daily BTC close used by production
// indicators and backtests. Volume is available only for Binance-covered days.
type BTCDailyPoint struct {
	Date   string          `json:"date"`
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume,omitempty"`
	Source string          `json:"source,omitempty"`
}

type btcDailyHistoryCache struct {
	SchemaVersion int             `json:"schema_version"`
	UpdatedAt     string          `json:"updated_at"`
	Points        []BTCDailyPoint `json:"points"`
}

type coinMetricsPricePoint struct {
	Asset    string `json:"asset"`
	Time     string `json:"time"`
	PriceUSD string `json:"PriceUSD"`
}

type coinMetricsPriceResp struct {
	Data        []coinMetricsPricePoint `json:"data"`
	NextPageURL string                  `json:"next_page_url"`
}

// BTCDailyHistoryCachePath returns the on-disk cache path for full BTC daily
// history. GUANFU_BTC_KLINE_CACHE is intentionally compatible with backtest
// workflows that want a stable, explicit kline cache.
func BTCDailyHistoryCachePath(cacheDir string) string {
	if path := strings.TrimSpace(os.Getenv("GUANFU_BTC_KLINE_CACHE")); path != "" {
		return expandHome(path)
	}
	if cacheDir == "" {
		cacheDir = cache.DefaultDir()
	}
	return filepath.Join(cacheDir, btcDailyHistoryCacheFile)
}

func (c *RealClient) loadOrUpdateBTCDailyHistory(ctx context.Context) ([]BTCDailyPoint, error) {
	return LoadOrUpdateBTCDailyHistory(ctx, c.cacheDir)
}

// LoadOrUpdateBTCDailyHistory loads the persistent BTC daily cache, seeds it
// from CoinMetrics PriceUSD when needed, and overlays recent Binance candles so
// the latest daily/intraday close is refreshed on each uncached run.
func LoadOrUpdateBTCDailyHistory(ctx context.Context, cacheDir string) ([]BTCDailyPoint, error) {
	path := BTCDailyHistoryCachePath(cacheDir)

	points, cacheErr := LoadBTCDailyHistoryCache(path)
	if cacheErr != nil && !os.IsNotExist(cacheErr) {
		// A corrupt cache should not permanently block a fresh rebuild.
		points = nil
	}

	hc := &http.Client{Timeout: 20 * time.Second}
	if ok, _ := btcDailyHistoryCoversFullRange(points, time.Now()); !ok {
		full, err := FetchCoinMetricsBTCPriceUSDHistory(ctx, hc, btcFullHistoryStartDate(), time.Now().UTC())
		if err != nil {
			if len(points) == 0 {
				return nil, err
			}
		} else {
			points = mergeBTCDailyHistory(points, full)
		}
	}

	recent, err := fetchBinanceBTCDailyHistory(ctx, hc, btcRecentRefreshDays)
	if err == nil && len(recent) > 0 {
		points = mergeBTCDailyHistory(points, recent)
	} else if len(points) == 0 {
		return nil, fmt.Errorf("binance BTC daily refresh failed with no cached history: %w", err)
	}

	points = normalizeBTCDailyHistory(points)
	if ok, reason := btcDailyHistoryCoversFullRange(points, time.Now()); !ok {
		return nil, fmt.Errorf("BTC daily history is incomplete: %s", reason)
	}

	if err := SaveBTCDailyHistoryCache(path, points); err != nil {
		return nil, err
	}
	return points, nil
}

func LoadBTCDailyHistoryCache(path string) ([]BTCDailyPoint, error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}

	var wrapped btcDailyHistoryCache
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Points) > 0 {
		if wrapped.SchemaVersion != 0 && wrapped.SchemaVersion != btcDailyHistoryCacheSchema {
			return nil, fmt.Errorf("BTC daily cache schema %d != %d", wrapped.SchemaVersion, btcDailyHistoryCacheSchema)
		}
		return normalizeBTCDailyHistory(wrapped.Points), nil
	}

	// Backward-compatible cache format used by guanfu-backtest:
	// {"YYYY-MM-DD": close_price, ...}
	var legacy map[string]float64
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	points := make([]BTCDailyPoint, 0, len(legacy))
	for date, close := range legacy {
		if close <= 0 || math.IsNaN(close) || math.IsInf(close, 0) {
			continue
		}
		points = append(points, BTCDailyPoint{
			Date:   date,
			Close:  decimal.NewFromFloat(close),
			Source: "legacy:kline_cache",
		})
	}
	return normalizeBTCDailyHistory(points), nil
}

func SaveBTCDailyHistoryCache(path string, points []BTCDailyPoint) error {
	path = expandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload := btcDailyHistoryCache{
		SchemaVersion: btcDailyHistoryCacheSchema,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		Points:        normalizeBTCDailyHistory(points),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return cache.WriteFileAtomic(path, data, 0o644)
}

func FetchCoinMetricsBTCPriceUSDHistory(ctx context.Context, hc *http.Client, from, to time.Time) ([]BTCDailyPoint, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}

	base := "https://community-api.coinmetrics.io/v4/timeseries/asset-metrics"
	params := url.Values{}
	params.Set("assets", "btc")
	params.Set("metrics", "PriceUSD")
	params.Set("frequency", "1d")
	params.Set("start_time", from.UTC().Format("2006-01-02"))
	params.Set("end_time", to.UTC().Format("2006-01-02"))
	params.Set("limit_per_asset", strconv.Itoa(coinMetricsPriceLookback))
	params.Set("page_size", strconv.Itoa(coinMetricsPriceLookback))

	nextURL := base + "?" + params.Encode()
	var out []BTCDailyPoint
	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")

		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("coinmetrics PriceUSD http %d: %s", resp.StatusCode, string(body[:minInt(len(body), 512)]))
		}

		var parsed coinMetricsPriceResp
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		for _, p := range parsed.Data {
			t, err := time.Parse(time.RFC3339Nano, p.Time)
			if err != nil {
				continue
			}
			close, err := decimal.NewFromString(p.PriceUSD)
			if err != nil || close.LessThanOrEqual(decimal.Zero) {
				continue
			}
			out = append(out, BTCDailyPoint{
				Date:   t.UTC().Format("2006-01-02"),
				Close:  close,
				Source: "coinmetrics:PriceUSD",
			})
		}
		nextURL = parsed.NextPageURL
	}

	out = normalizeBTCDailyHistory(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("coinmetrics PriceUSD returned empty BTC history")
	}
	return out, nil
}

func fetchBinanceBTCDailyHistory(ctx context.Context, hc *http.Client, targetDays int) ([]BTCDailyPoint, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	if targetDays <= 0 {
		targetDays = btcRecentRefreshDays
	}
	url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=BTCUSDT&interval=1d&limit=%d", targetDays)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance BTC kline http %d: %s", resp.StatusCode, string(body[:minInt(len(body), 512)]))
	}
	var raw [][]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]BTCDailyPoint, 0, len(raw))
	for _, row := range raw {
		p, ok := btcDailyPointFromBinanceRow(row)
		if ok {
			out = append(out, p)
		}
	}
	return normalizeBTCDailyHistory(out), nil
}

func btcDailyPointFromBinanceRow(row []interface{}) (BTCDailyPoint, bool) {
	if len(row) < 6 {
		return BTCDailyPoint{}, false
	}
	openTime, ok := klineOpenTimeMillis(row)
	if !ok {
		return BTCDailyPoint{}, false
	}
	closeStr, ok := row[4].(string)
	if !ok {
		return BTCDailyPoint{}, false
	}
	close, err := decimal.NewFromString(closeStr)
	if err != nil || close.LessThanOrEqual(decimal.Zero) {
		return BTCDailyPoint{}, false
	}
	volume := decimal.Zero
	if volumeStr, ok := row[5].(string); ok {
		if v, err := decimal.NewFromString(volumeStr); err == nil {
			volume = v
		}
	}
	return BTCDailyPoint{
		Date:   time.UnixMilli(openTime).UTC().Format("2006-01-02"),
		Close:  close,
		Volume: volume,
		Source: "binance:BTCUSDT",
	}, true
}

func applyBTCDailyHistoryToSnapshot(points []BTCDailyPoint, snap *model.MarketSnapshot) error {
	points = normalizeBTCDailyHistory(points)
	if len(points) == 0 {
		return fmt.Errorf("empty BTC daily history")
	}

	n := len(points)
	snap.BTCPriceHistory = make([]decimal.Decimal, n)
	snap.BTCVolumeHistory = make([]decimal.Decimal, n)
	for i, p := range points {
		idx := n - 1 - i
		snap.BTCPriceHistory[idx] = p.Close
		snap.BTCVolumeHistory[idx] = p.Volume
	}

	latest := points[n-1]
	snap.BTCPrice = latest.Close
	snap.BTCVolume24h = latest.Volume
	if t, err := time.Parse("2006-01-02", latest.Date); err == nil {
		snap.BTCPriceAsOf = t.UTC().Format(time.RFC3339)
	} else {
		snap.BTCPriceAsOf = latest.Date
	}
	return nil
}

func normalizeBTCDailyHistory(points []BTCDailyPoint) []BTCDailyPoint {
	byDate := make(map[string]BTCDailyPoint, len(points))
	for _, p := range points {
		t, err := time.Parse("2006-01-02", p.Date)
		if err != nil || p.Close.LessThanOrEqual(decimal.Zero) {
			continue
		}
		p.Date = t.UTC().Format("2006-01-02")
		byDate[p.Date] = p
	}
	out := make([]BTCDailyPoint, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func mergeBTCDailyHistory(base, updates []BTCDailyPoint) []BTCDailyPoint {
	merged := make([]BTCDailyPoint, 0, len(base)+len(updates))
	merged = append(merged, base...)
	merged = append(merged, updates...)
	return normalizeBTCDailyHistory(merged)
}

func btcDailyHistoryCoversFullRange(points []BTCDailyPoint, now time.Time) (bool, string) {
	points = normalizeBTCDailyHistory(points)
	if len(points) == 0 {
		return false, "empty history"
	}

	minDays := btcMinFullHistoryDays(now)
	if len(points) < minDays {
		return false, fmt.Sprintf("has %d days, need at least %d days from %s", len(points), minDays, BTCFullHistoryStart)
	}

	first, _ := time.Parse("2006-01-02", points[0].Date)
	start := btcFullHistoryStartDate()
	if first.After(start.AddDate(0, 0, btcHistoryFreshSlackDays)) {
		return false, fmt.Sprintf("starts at %s, need %s", points[0].Date, BTCFullHistoryStart)
	}

	last, _ := time.Parse("2006-01-02", points[len(points)-1].Date)
	requiredLatest := btcLatestRequiredDate(now)
	if last.Before(requiredLatest.AddDate(0, 0, -btcHistoryFreshSlackDays)) {
		return false, fmt.Sprintf("latest daily close is %s, need at least %s", points[len(points)-1].Date, requiredLatest.Format("2006-01-02"))
	}
	return true, ""
}

func btcMinFullHistoryDays(now time.Time) int {
	start := btcFullHistoryStartDate()
	latest := btcLatestRequiredDate(now)
	if latest.Before(start) {
		return btcHistoryMinFreshDays
	}
	days := int(latest.Sub(start).Hours()/24) + 1 - btcHistoryFreshSlackDays
	if days < btcHistoryMinFreshDays {
		return btcHistoryMinFreshDays
	}
	return days
}

func btcFullHistoryStartDate() time.Time {
	t, _ := time.Parse("2006-01-02", BTCFullHistoryStart)
	return t.UTC()
}

func btcLatestRequiredDate(now time.Time) time.Time {
	todayUTC := now.UTC().Truncate(24 * time.Hour)
	return todayUTC.AddDate(0, 0, -1)
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if len(path) == 1 {
		return home
	}
	if path[1] == '/' {
		return filepath.Join(home, path[2:])
	}
	return path
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
