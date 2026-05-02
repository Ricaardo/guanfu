// fetch_cross_asset.go — 拉取黄金、QQQ、SPY 价格数据用于跨资产对比。
//
// 数据源:
//   - 黄金: Binance PAXG/USDT (tokenized gold, 无 API key, 稳定)
//   - QQQ:  Yahoo Finance (纳斯达克 100 ETF)
//   - SPY:  Yahoo Finance (标普 500 ETF)
//
// 价格历史拉取量与 BTC 对齐 (3000d)，用于计算相关性和相对强弱。

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// yahooChartResp Yahoo Finance v8 chart API response
type yahooChartResp struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

// FetchCrossAssetData 拉取黄金、QQQ、SPY 的近期价格和历史。
//
// 数据源优先级:
//   - 黄金: Binance PAXG/USDT (tokenized gold)
//   - QQQ/SPY: Futu OpenD (本地网关) > Yahoo Finance (公网备份)
//
// 环境变量:
//   FUTU_GATEWAY=127.0.0.1:11111  (默认)
//   FUTU_ENABLED=0                禁用富途，直接用 Yahoo
func FetchCrossAssetData(ctx context.Context, targetDays int) (*CrossAssetPrices, error) {
	if targetDays <= 0 {
		targetDays = 3000
	}
	hc := &http.Client{Timeout: 12 * time.Second}
	out := &CrossAssetPrices{}

	// 黄金: Binance PAXG/USDT klines
	if err := fetchBinancePAXG(ctx, hc, targetDays, out); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Binance PAXG fetch failed: %v", err))
	}

	// QQQ & SPY: 先试富途，再降级 Yahoo
	futuOK := false
	if os.Getenv("FUTU_ENABLED") != "0" {
		if futuData, err := FetchCrossAssetFromFutu(targetDays); err == nil && futuData != nil {
			out.QQQPrice = futuData.QQQPrice
			out.QQQHistory = futuData.QQQHistory
			out.QQQPriceAsOf = futuData.QQQPriceAsOf
			out.SPYPrice = futuData.SPYPrice
			out.SPYHistory = futuData.SPYHistory
			out.SPYPriceAsOf = futuData.SPYPriceAsOf
			out.Warnings = append(out.Warnings, futuData.Warnings...)
			if out.QQQPrice > 0 && out.SPYPrice > 0 {
				futuOK = true
			}
		} else if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("Futu fetch failed (will try Yahoo): %v", err))
		}
	}

	if !futuOK {
		// Yahoo Finance fallback
		type result struct {
			symbol  string
			price   float64
			history []float64
			asOf    string
			err     error
		}
		ch := make(chan result, 2)
		for _, sym := range []string{"QQQ", "SPY"} {
			go func(symbol string) {
				p, h, a, e := fetchYahooChart(ctx, hc, symbol, targetDays)
				ch <- result{symbol, p, h, a, e}
			}(sym)
		}
		for i := 0; i < 2; i++ {
			r := <-ch
			if r.err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("Yahoo %s: %v", r.symbol, r.err))
				continue
			}
			switch r.symbol {
			case "QQQ":
				out.QQQPrice = r.price
				out.QQQHistory = r.history
				out.QQQPriceAsOf = r.asOf
			case "SPY":
				out.SPYPrice = r.price
				out.SPYHistory = r.history
				out.SPYPriceAsOf = r.asOf
			}
		}
	}

	return out, nil
}

// fetchBinancePAXG 从 Binance 拉取 PAXG/USDT 价格历史和最新价。
// 使用与 BTC/ETH 相同的 klines API，免费、无频率限制。
func fetchBinancePAXG(ctx context.Context, hc *http.Client, targetDays int, out *CrossAssetPrices) error {
	urlFmt := "https://api.binance.com/api/v3/klines?symbol=PAXGUSDT&interval=1d&limit=%d"
	var all [][]interface{}

	// 拉取历史 (分页)
	endTime := time.Now().UnixMilli()
	for len(all) < targetDays {
		limit := 1000
		if remaining := targetDays - len(all); remaining < limit {
			limit = remaining
		}
		reqURL := fmt.Sprintf(urlFmt, limit) + "&endTime=" + strconv.FormatInt(endTime, 10)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		var batch [][]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&batch); err != nil {
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		if len(batch) == 0 {
			break
		}
		all = append(batch, all...)
		// 取第一批的 openTime 作为下页 endTime
		if ot, ok := batch[0][0].(float64); ok {
			endTime = int64(ot) - 1
		} else {
			break
		}
		if len(batch) < limit {
			break
		}
	}

	if len(all) == 0 {
		return fmt.Errorf("empty PAXG klines")
	}
	if len(all) > targetDays {
		all = all[len(all)-targetDays:]
	}

	n := len(all)
	history := make([]float64, n)
	// Reverse: index 0 = newest
	for i, k := range all {
		idx := n - 1 - i
		if closeStr, ok := k[4].(string); ok {
			if v, err := strconv.ParseFloat(closeStr, 64); err == nil {
				history[idx] = v
			}
		}
		if idx == 0 {
			if ot, ok := k[0].(float64); ok {
				out.GoldPriceAsOf = time.UnixMilli(int64(ot)).UTC().Format("2006-01-02")
			}
		}
	}

	out.GoldPrice = history[0]
	out.GoldHistory = history
	return nil
}

func fetchYahooChart(ctx context.Context, hc *http.Client, symbol string, targetDays int) (price float64, history []float64, asOf string, err error) {
	now := time.Now().Unix()
	from := now - int64(targetDays*86400)

	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("period1", fmt.Sprintf("%d", from))
	params.Set("period2", fmt.Sprintf("%d", now))
	params.Set("interval", "1d")
	params.Set("includePrePost", "false")

	apiURL := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?%s", symbol, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return 0, nil, "", fmt.Errorf("yahoo %s http %d: %s", symbol, resp.StatusCode, string(body))
	}

	var parsed yahooChartResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, nil, "", err
	}
	if len(parsed.Chart.Result) == 0 || len(parsed.Chart.Result[0].Indicators.Quote) == 0 {
		return 0, nil, "", fmt.Errorf("yahoo %s empty result", symbol)
	}

	result := parsed.Chart.Result[0]
	closes := result.Indicators.Quote[0].Close

	history = make([]float64, len(closes))
	for i, c := range closes {
		if c != nil {
			history[i] = *c
		}
	}

	for i := len(history) - 1; i >= 0; i-- {
		if history[i] > 0 {
			price = history[i]
			if i < len(result.Timestamp) {
				asOf = time.Unix(result.Timestamp[i], 0).UTC().Format("2006-01-02")
			}
			break
		}
	}

	return price, history, asOf, nil
}

// CrossAssetPrices 跨资产价格数据聚合
type CrossAssetPrices struct {
	GoldPrice     float64
	GoldHistory   []float64
	GoldPriceAsOf string
	QQQPrice      float64
	QQQHistory    []float64
	QQQPriceAsOf  string
	SPYPrice      float64
	SPYHistory    []float64
	SPYPriceAsOf  string
	Warnings      []string
}
