// SoSoValue 客户端 — 拿现货 BTC ETF 净流入数据
//
// 免费公开 API。POST `https://api.sosovalue.xyz/openapi/v2/etf/historicalInflowChart`
// body: {"type": "us-btc-spot"}
// 返回每日净流入数组（newest 在前）。

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// ETFFlow 单日 ETF 数据
type ETFFlow struct {
	Date           time.Time
	NetInflowUSD   float64
	TotalAssetsUSD float64
	CumulativeUSD  float64
}

// ETFData 整理后的 ETF 数据汇总
type ETFData struct {
	NetInflow7dUSD   float64   `json:"net_inflow_7d_usd"`
	NetInflow30dUSD  float64   `json:"net_inflow_30d_usd"`
	NetInflowMA7dUSD float64   `json:"net_inflow_ma7d_usd"` // 7 日均值（vs 30d 比较）
	TotalAssetsUSD   float64   `json:"total_assets_usd"`
	LatestDate       time.Time `json:"-"`
	StaleDays        int       `json:"-"` // 距今天数（>=2 警告）
}

// FetchBTCETFData 拉 SoSoValue ETF 数据并汇总 7d/30d
func FetchBTCETFData(ctx context.Context) (*ETFData, error) {
	httpClient := &http.Client{Timeout: 8 * time.Second}

	body := []byte(`{"type":"us-btc-spot"}`)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.sosovalue.xyz/openapi/v2/etf/historicalInflowChart",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Date            string  `json:"date"`
			TotalNetInflow  float64 `json:"totalNetInflow"`
			TotalNetAssets  float64 `json:"totalNetAssets"`
			CumNetInflow    float64 `json:"cumNetInflow"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 0 || len(parsed.Data) == 0 {
		return nil, fmt.Errorf("sosovalue err: code=%d msg=%s", parsed.Code, parsed.Msg)
	}

	// 解析 + 按日期升序
	flows := make([]ETFFlow, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		t, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			continue
		}
		flows = append(flows, ETFFlow{
			Date:           t,
			NetInflowUSD:   d.TotalNetInflow,
			TotalAssetsUSD: d.TotalNetAssets,
			CumulativeUSD:  d.CumNetInflow,
		})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date.Before(flows[j].Date) })

	if len(flows) == 0 {
		return nil, fmt.Errorf("empty etf flows")
	}

	out := &ETFData{
		LatestDate: flows[len(flows)-1].Date,
		StaleDays:  int(time.Since(flows[len(flows)-1].Date).Hours() / 24),
	}

	// 累计 7 日 / 30 日
	for i := len(flows) - 1; i >= 0 && i >= len(flows)-30; i-- {
		days := len(flows) - 1 - i
		if days < 7 {
			out.NetInflow7dUSD += flows[i].NetInflowUSD
		}
		out.NetInflow30dUSD += flows[i].NetInflowUSD
	}
	out.NetInflowMA7dUSD = out.NetInflow7dUSD / 7
	out.TotalAssetsUSD = flows[len(flows)-1].TotalAssetsUSD

	return out, nil
}
