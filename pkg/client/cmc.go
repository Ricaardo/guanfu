// CoinMarketCap market-context refresh source.
//
// Scope: market reading only. CMC is useful for independent crypto market
// context (global metrics, BTC quote, exchange/DEX coverage later), but this
// source deliberately does not replace the canonical BTC CoinMetrics+Binance
// price history or any forecast feature bundle.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	cmcAPIKeyEnv    = "CMC_API_KEY"
	cmcAPIKeyEnvAlt = "CMC_PRO_API_KEY"
	cmcSourceKey    = "cmc_market_context"
	cmcTimeout      = 30 * time.Second
)

var cmcBaseURL = "https://pro-api.coinmarketcap.com"

// CMCMarketContextSource pulls a small latest-only market context set from
// CoinMarketCap. It stores daily snapshots under cmc_* PriceStore keys.
type CMCMarketContextSource struct{}

func (CMCMarketContextSource) Key() string { return cmcSourceKey }
func (CMCMarketContextSource) DisplayName() string {
	return "cmc_market_context (CMC global metrics + BTC quote)"
}

func (c CMCMarketContextSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, "cmc_total_market_cap_usd")
	apiKey := cmcAPIKey()
	if apiKey == "" {
		count, _ := s.Count("cmc_total_market_cap_usd")
		return &RefreshResult{
			Key:         c.Key(),
			DisplayName: c.DisplayName(),
			Mode:        "skip",
			SkipReason:  "config",
			Stale:       stale,
			Action:      "configure",
			Total:       count,
			LastDate:    lastDate,
			Error:       cmcAPIKeyEnv + " not set",
		}, nil
	}
	if !stale {
		count, _ := s.Count("cmc_total_market_cap_usd")
		return &RefreshResult{
			Key: c.Key(), DisplayName: c.DisplayName(),
			Mode: "skip", SkipReason: "fresh", Action: "ignore",
			Total: count, LastDate: lastDate,
		}, nil
	}

	points, err := fetchCMCMarketContext(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		count, _ := s.Count("cmc_total_market_cap_usd")
		return &RefreshResult{
			Key: c.Key(), DisplayName: c.DisplayName(),
			Mode: "incremental", SkipReason: "no_new_data",
			Total: count, LastDate: lastDate,
		}, nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}
	added := 0
	for key, pt := range points {
		before, _ := s.Count(key)
		if err := s.Append(key, []store.PricePoint{pt}); err != nil {
			return nil, fmt.Errorf("%s append: %w", key, err)
		}
		after, _ := s.Count(key)
		if after > before {
			added += after - before
		}
	}
	total, _ := s.Count("cmc_total_market_cap_usd")
	last, _ := s.LastDate("cmc_total_market_cap_usd")
	return &RefreshResult{
		Key: c.Key(), DisplayName: c.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

func fetchCMCMarketContext(ctx context.Context, apiKey string) (map[string]store.PricePoint, error) {
	global, err := fetchCMCGlobalMetrics(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	btc, err := fetchCMCBTCQuote(ctx, apiKey)
	if err != nil {
		return nil, err
	}

	out := map[string]store.PricePoint{}
	addPoint := func(key string, value float64, date, source string) {
		if value <= 0 || date == "" {
			return
		}
		out[key] = store.PricePoint{Date: date, Close: value, Source: source}
	}

	gDate := dateFromRFC3339(global.Quote.USD.LastUpdated)
	addPoint("cmc_total_market_cap_usd", global.Quote.USD.TotalMarketCap, gDate, "cmc:global-metrics")
	addPoint("cmc_total_volume_24h_usd", global.Quote.USD.TotalVolume24h, gDate, "cmc:global-metrics")
	addPoint("cmc_altcoin_market_cap_usd", global.Quote.USD.AltcoinMarketCap, gDate, "cmc:global-metrics")
	addPoint("cmc_altcoin_volume_24h_usd", global.Quote.USD.AltcoinVolume24h, gDate, "cmc:global-metrics")
	addPoint("cmc_btc_dominance_pct", global.BTCDominance, gDate, "cmc:global-metrics")
	addPoint("cmc_eth_dominance_pct", global.ETHDominance, gDate, "cmc:global-metrics")

	bDate := dateFromRFC3339(btc.Quote.USD.LastUpdated)
	addPoint("cmc_btc_price_usd", btc.Quote.USD.Price, bDate, "cmc:cryptocurrency-quotes")
	addPoint("cmc_btc_volume_24h_usd", btc.Quote.USD.Volume24h, bDate, "cmc:cryptocurrency-quotes")
	addPoint("cmc_btc_market_cap_usd", btc.Quote.USD.MarketCap, bDate, "cmc:cryptocurrency-quotes")
	return out, nil
}

type cmcStatus struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

type cmcGlobalResp struct {
	Status cmcStatus     `json:"status"`
	Data   cmcGlobalData `json:"data"`
}

type cmcGlobalData struct {
	BTCDominance float64 `json:"btc_dominance"`
	ETHDominance float64 `json:"eth_dominance"`
	Quote        struct {
		USD struct {
			TotalMarketCap   float64 `json:"total_market_cap"`
			TotalVolume24h   float64 `json:"total_volume_24h"`
			AltcoinMarketCap float64 `json:"altcoin_market_cap"`
			AltcoinVolume24h float64 `json:"altcoin_volume_24h"`
			LastUpdated      string  `json:"last_updated"`
		} `json:"USD"`
	} `json:"quote"`
}

type cmcQuoteResp struct {
	Status cmcStatus               `json:"status"`
	Data   map[string]cmcQuoteCoin `json:"data"`
}

type cmcQuoteCoin struct {
	ID     int    `json:"id"`
	Symbol string `json:"symbol"`
	Quote  struct {
		USD struct {
			Price       float64 `json:"price"`
			Volume24h   float64 `json:"volume_24h"`
			MarketCap   float64 `json:"market_cap"`
			LastUpdated string  `json:"last_updated"`
		} `json:"USD"`
	} `json:"quote"`
}

func fetchCMCGlobalMetrics(ctx context.Context, apiKey string) (cmcGlobalData, error) {
	var out cmcGlobalResp
	err := fetchCMCJSON(ctx, apiKey, "/v1/global-metrics/quotes/latest", url.Values{"convert": []string{"USD"}}, &out)
	if err != nil {
		return cmcGlobalData{}, err
	}
	if out.Status.ErrorCode != 0 {
		return cmcGlobalData{}, fmt.Errorf("cmc global metrics: %s", out.Status.ErrorMessage)
	}
	return out.Data, nil
}

func fetchCMCBTCQuote(ctx context.Context, apiKey string) (cmcQuoteCoin, error) {
	var out cmcQuoteResp
	err := fetchCMCJSON(ctx, apiKey, "/v2/cryptocurrency/quotes/latest", url.Values{
		"id":      []string{"1"},
		"convert": []string{"USD"},
	}, &out)
	if err != nil {
		return cmcQuoteCoin{}, err
	}
	if out.Status.ErrorCode != 0 {
		return cmcQuoteCoin{}, fmt.Errorf("cmc btc quote: %s", out.Status.ErrorMessage)
	}
	coin, ok := out.Data["1"]
	if !ok {
		return cmcQuoteCoin{}, fmt.Errorf("cmc btc quote: missing id=1 in response")
	}
	return coin, nil
}

func fetchCMCJSON(ctx context.Context, apiKey, path string, q url.Values, target any) error {
	u := strings.TrimRight(cmcBaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CMC_PRO_API_KEY", apiKey)
	req.Header.Set("User-Agent", "guanfu/1.0 (CMC market context)")

	resp, err := (&http.Client{Timeout: cmcTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("cmc fetch %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cmc %s http %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("cmc %s json: %w", path, err)
	}
	return nil
}

func cmcAPIKey() string {
	if key := strings.TrimSpace(os.Getenv(cmcAPIKeyEnv)); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv(cmcAPIKeyEnvAlt)); key != "" {
		return key
	}
	path := os.Getenv("GUANFU_ENV_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		path = filepath.Join(home, ".guanfu", "env")
	}
	if key := readEnvFileValue(path, cmcAPIKeyEnv); key != "" {
		return key
	}
	return readEnvFileValue(path, cmcAPIKeyEnvAlt)
}

func readEnvFileValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	exportPrefix := "export " + prefix
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, exportPrefix):
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, exportPrefix)), `"'`)
		case strings.HasPrefix(line, prefix):
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"'`)
		}
	}
	return ""
}

func dateFromRFC3339(value string) string {
	if value == "" {
		return time.Now().UTC().Format("2006-01-02")
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now().UTC().Format("2006-01-02")
	}
	return t.UTC().Format("2006-01-02")
}
