// FRED (St. Louis Fed) macro data fetcher for coinman.
//
// 拉 4 个序列：
//   - DTWEXBGS: Trade-Weighted USD Index (Broad)，daily — 用作 DXY 代理
//   - DFII10:   10-Year TIPS 收益率，daily
//   - M2SL:     M2 货币供应（季调），monthly — YoY 同比
//   - SP500:    标普 500，daily — 与 BTC 做 30d 收益相关
//
// 需要环境变量 FRED_API_KEY。无 key 时 FetchMacroData 返回 nil + nil error，
// 由 RealClient 决定是否填 placeholder。
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
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

// MacroData 4 个 FRED 衍生指标 + 时效性
type MacroData struct {
	DXY60dTrendPct    float64 // (DTWEXBGS_t - DTWEXBGS_{t-60}) / DTWEXBGS_{t-60} * 100
	DXYLatest         float64 // 最新 DTWEXBGS 值（用于显示）
	DXYAsOf           string  // 最新值日期 YYYY-MM-DD
	RealYield10YPct   float64 // DFII10 最新值（已是百分比）
	RealYield10YAsOf  string
	M2YoYPct          float64 // (M2SL_t - M2SL_{t-12mo}) / M2SL_{t-12mo} * 100
	M2LatestB         float64 // 最新 M2 (单位：十亿美元)
	M2AsOf            string
	SPXCorrelation30d float64 // BTC 与 SPX 日收益的 Pearson 相关，[-1, 1]
	SPXAsOf           string
	StaleWarnings     []string
}

// fredObservation 单个数据点
type fredObservation struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

type fredObservationsResp struct {
	Observations []fredObservation `json:"observations"`
}

const fredObservationsURL = "https://api.stlouisfed.org/fred/series/observations"

// FetchMacroData 并发拉 4 个序列并计算衍生指标。
// btcHistoryNewestFirst: BTC 历史价格（idx 0 = 今天，与 RealClient 一致），用于 SPX 相关。
// 没设 FRED_API_KEY → 返回 (nil, nil)。
func FetchMacroData(ctx context.Context, btcHistoryNewestFirst []decimal.Decimal) (*MacroData, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		return nil, nil
	}

	hc := &http.Client{Timeout: 10 * time.Second}

	type result struct {
		series string
		obs    []fredObservation
		err    error
	}
	ch := make(chan result, 4)

	fetch := func(series string, limit int) {
		obs, err := getFREDObservations(ctx, hc, apiKey, series, limit)
		ch <- result{series: series, obs: obs, err: err}
	}

	go fetch("DTWEXBGS", 90)  // 60d 趋势 + buffer
	go fetch("DFII10", 1)     // 仅最新
	go fetch("M2SL", 14)      // YoY 同比，留 buffer
	go fetch("SP500", 45)     // 30 trading days + 周末 buffer

	results := map[string][]fredObservation{}
	var stales []string
	for i := 0; i < 4; i++ {
		r := <-ch
		if r.err != nil {
			stales = append(stales, fmt.Sprintf("%s 拉取失败: %v", r.series, r.err))
			continue
		}
		results[r.series] = r.obs
	}

	out := &MacroData{StaleWarnings: stales}

	if obs := results["DTWEXBGS"]; len(obs) >= 61 {
		latest := parseFloat(obs[0].Value)
		past := parseFloat(obs[60].Value)
		if latest > 0 && past > 0 {
			out.DXYLatest = latest
			out.DXY60dTrendPct = (latest - past) / past * 100
			out.DXYAsOf = obs[0].Date
		}
	}

	if obs := results["DFII10"]; len(obs) >= 1 {
		v := parseFloat(obs[0].Value)
		if !math.IsNaN(v) && obs[0].Value != "." {
			out.RealYield10YPct = v
			out.RealYield10YAsOf = obs[0].Date
		}
	}

	if obs := results["M2SL"]; len(obs) >= 13 {
		latest := parseFloat(obs[0].Value)
		past := parseFloat(obs[12].Value)
		if latest > 0 && past > 0 {
			out.M2LatestB = latest
			out.M2YoYPct = (latest - past) / past * 100
			out.M2AsOf = obs[0].Date
		}
	}

	if obs := results["SP500"]; len(obs) >= 31 && len(btcHistoryNewestFirst) > 60 {
		corr, asof, ok := computeBTCSPXCorrelation30d(obs, btcHistoryNewestFirst)
		if ok {
			out.SPXCorrelation30d = corr
			out.SPXAsOf = asof
		}
	}

	return out, nil
}

// getFREDObservations 拉单个 series 的最新 N 个观测，desc 排序（idx 0 = 最新）
func getFREDObservations(ctx context.Context, hc *http.Client, apiKey, seriesID string, limit int) ([]fredObservation, error) {
	params := url.Values{}
	params.Set("series_id", seriesID)
	params.Set("api_key", apiKey)
	params.Set("file_type", "json")
	params.Set("limit", strconv.Itoa(limit))
	params.Set("sort_order", "desc")

	req, err := http.NewRequestWithContext(ctx, "GET", fredObservationsURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r fredObservationsResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return r.Observations, nil
}

// computeBTCSPXCorrelation30d 用最近 31 个 SPX 交易日 + BTC 同日收盘做日收益相关
//
// SPX obs 是 desc 排序（idx 0 = 最新）。BTC 历史也是 newest first，但 BTC 7×24，
// SPX 5×7。我们按 SPX 的日期匹配 BTC 的天数偏移（today.Sub(SPXdate).Days）。
func computeBTCSPXCorrelation30d(spxObs []fredObservation, btcHistory []decimal.Decimal) (float64, string, bool) {
	now := time.Now().UTC().Truncate(24 * time.Hour)

	type pair struct{ btc, spx float64 }
	pairs := make([]pair, 0, 31)

	for i := 0; i < len(spxObs) && len(pairs) < 31; i++ {
		val := parseFloat(spxObs[i].Value)
		if val <= 0 || spxObs[i].Value == "." {
			continue
		}
		date, err := time.Parse("2006-01-02", spxObs[i].Date)
		if err != nil {
			continue
		}
		offset := int(now.Sub(date).Hours() / 24)
		if offset < 0 || offset >= len(btcHistory) {
			continue
		}
		btc, _ := btcHistory[offset].Float64()
		if btc <= 0 {
			continue
		}
		pairs = append(pairs, pair{btc: btc, spx: val})
	}

	if len(pairs) < 31 {
		return 0, "", false
	}

	// pairs 当前是 newest-first；计算日收益要 chrono 顺序
	btcRet := make([]float64, 0, 30)
	spxRet := make([]float64, 0, 30)
	for i := 0; i < 30; i++ {
		// pairs[i] 是 newer，pairs[i+1] 是 older（desc 排）
		newer := pairs[i]
		older := pairs[i+1]
		if older.btc <= 0 || older.spx <= 0 {
			continue
		}
		btcRet = append(btcRet, math.Log(newer.btc/older.btc))
		spxRet = append(spxRet, math.Log(newer.spx/older.spx))
	}
	if len(btcRet) < 20 {
		return 0, "", false
	}
	return pearson(btcRet, spxRet), spxObs[0].Date, true
}

// pearson 皮尔逊相关系数
func pearson(xs, ys []float64) float64 {
	n := len(xs)
	if n != len(ys) || n == 0 {
		return 0
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += xs[i]
		sy += ys[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var num, dx2, dy2 float64
	for i := 0; i < n; i++ {
		dx := xs[i] - mx
		dy := ys[i] - my
		num += dx * dy
		dx2 += dx * dx
		dy2 += dy * dy
	}
	denom := math.Sqrt(dx2 * dy2)
	if denom == 0 {
		return 0
	}
	return num / denom
}

func parseFloat(s string) float64 {
	if s == "" || s == "." {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}
