// guanfu-backtest — 把 verdict 引擎拿到历史日期上回放，验证 stance/proximity 的预测力。
//
// 用法：
//   guanfu-backtest --start 2022-01-01 --end 2026-04-01 --interval 7
//   guanfu-backtest --start 2024-01-01 --end 2026-04-01 --interval 1 --json > result.json
//
// 数据源：
//   - BTC daily kline 从 Binance 公开 API 直拉（可缓存到 ./cache/）
//   - 仅用 kline 衍生的指标做回测：mayer_multiple, sma_200w_dev, pi_cycle_top_ratio,
//     rsi_14, macd_histogram, ma_alignment, ahr999 简化版
//   - 其他指标（ETF / 资金费率 / DVOL / 链上）在历史日期上为 Missing → 自动跳过
//   - 这是诚实的低覆盖率回测；如果想做更高覆盖率，需要补充 CoinMetrics 历史 MVRV/NUPL 拉取
//
// 输出：按 stance 分桶的 hit rate + avg fwd return，以及按 top/bottom proximity
// 分桶的预测力验证。

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/Ricaardo/guanfu/internal/engine"
	"github.com/Ricaardo/guanfu/internal/model"
)

func main() {
	startStr := flag.String("start", "", "起始日期 YYYY-MM-DD（默认 4 年前）")
	endStr := flag.String("end", time.Now().Format("2006-01-02"), "结束日期 YYYY-MM-DD")
	interval := flag.Int("interval", 7, "采样间隔天数（1=daily, 7=weekly）")
	jsonOut := flag.Bool("json", false, "JSON 输出（默认人类报告）")
	includeRaw := flag.Bool("samples", false, "JSON 输出包含每个采样点（量大）")
	flag.Parse()

	if *startStr == "" {
		*startStr = time.Now().AddDate(-4, 0, 0).Format("2006-01-02")
	}

	startT, err := time.Parse("2006-01-02", *startStr)
	if err != nil {
		log.Fatalf("invalid --start: %v", err)
	}
	endT, err := time.Parse("2006-01-02", *endStr)
	if err != nil {
		log.Fatalf("invalid --end: %v", err)
	}
	if !endT.After(startT) {
		log.Fatalf("--end must be after --start")
	}

	// 拉 BTC kline 直到 endT；为算 sma_200w 和 ahr999 长基线，
	// 从 startT 再往前推 1500 天
	prelude := startT.AddDate(0, 0, -1500)
	log.Printf("fetching BTC daily kline %s → %s", prelude.Format("2006-01-02"), endT.Format("2006-01-02"))

	prices, err := fetchBTCDailyClose(prelude, endT)
	if err != nil {
		log.Fatalf("fetch kline: %v", err)
	}
	log.Printf("got %d daily closes", len(prices))

	// 索引：date string -> idx
	idx := map[string]int{}
	dates := make([]string, len(prices))
	closes := make([]float64, len(prices))
	for i, p := range prices {
		dates[i] = p.date.Format("2006-01-02")
		closes[i] = p.close
		idx[dates[i]] = i
	}
	prov := dateMapPrices(idx2map(idx, closes))

	// 采样
	var samples []engine.SamplePoint
	for d := startT; !d.After(endT); d = d.AddDate(0, 0, *interval) {
		dateStr := d.Format("2006-01-02")
		i, ok := idx[dateStr]
		if !ok {
			continue
		}
		panel := buildBacktestPanel(closes[:i+1], dateStr)
		v := engine.BuildVerdict(panel)
		sp := engine.SamplePoint{
			Date:            dateStr,
			Price:           closes[i],
			Stance:          v.Stance,
			Regime:          v.Regime,
			NetDirection:    v.NetDirection,
			Coverage:        v.Coverage,
			TopProximity:    v.TopProximity,
			BottomProximity: v.BottomProximity,
		}
		if r, ok := engine.ForwardReturn(prov, dateStr, 30); ok {
			sp.Fwd30dPct = r
			sp.HasFwd30 = true
		}
		if r, ok := engine.ForwardReturn(prov, dateStr, 90); ok {
			sp.Fwd90dPct = r
			sp.HasFwd90 = true
		}
		if r, ok := engine.ForwardReturn(prov, dateStr, 180); ok {
			sp.Fwd180dPct = r
			sp.HasFwd180 = true
		}
		samples = append(samples, sp)
	}

	report := engine.AggregateBacktest(samples, *includeRaw)

	if *jsonOut {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return
	}
	printReport(report)
}

// --- BTC kline fetcher ---

type pricePoint struct {
	date  time.Time
	close float64
}

func fetchBTCDailyClose(from, to time.Time) ([]pricePoint, error) {
	const limit = 1000
	var out []pricePoint
	cursor := to.UnixMilli()
	hc := &http.Client{Timeout: 20 * time.Second}
	startMs := from.UnixMilli()
	for cursor > startMs {
		url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=BTCUSDT&interval=1d&limit=%d&endTime=%d", limit, cursor)
		req, _ := http.NewRequest("GET", url, nil)
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var raw [][]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("parse: %v body=%s", err, string(body[:min(200, len(body))]))
		}
		if len(raw) == 0 {
			break
		}
		batch := make([]pricePoint, 0, len(raw))
		for _, row := range raw {
			ts, ok := row[0].(float64)
			if !ok {
				continue
			}
			closeStr, ok := row[4].(string)
			if !ok {
				continue
			}
			c, err := strconv.ParseFloat(closeStr, 64)
			if err != nil {
				continue
			}
			batch = append(batch, pricePoint{date: time.UnixMilli(int64(ts)).UTC().Truncate(24 * time.Hour), close: c})
		}
		if len(batch) == 0 {
			break
		}
		out = append(batch, out...)
		earliest := batch[0].date.UnixMilli()
		if earliest <= startMs {
			break
		}
		cursor = earliest - 1
		// guard against pagination loop on small batches
		if len(batch) < limit {
			break
		}
	}
	// dedupe + filter
	seen := map[string]bool{}
	filtered := out[:0]
	for _, p := range out {
		k := p.date.Format("2006-01-02")
		if seen[k] {
			continue
		}
		seen[k] = true
		if p.date.Before(from) || p.date.After(to) {
			continue
		}
		filtered = append(filtered, p)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].date.Before(filtered[j].date) })
	return filtered, nil
}

func idx2map(idx map[string]int, closes []float64) map[string]float64 {
	out := map[string]float64{}
	for k, i := range idx {
		out[k] = closes[i]
	}
	return out
}

type dateMapPrices map[string]float64

func (d dateMapPrices) PriceAt(date string) (float64, bool) {
	v, ok := d[date]
	return v, ok
}

// --- 历史日期重建 panel（仅 kline 衍生指标） ---

// buildBacktestPanel — 用 closes[:n+1] 切片（[0..i]，i 是当前日期索引）
// 重建一个最小 panel：只填 kline 可派生的指标。其他全设 Missing。
func buildBacktestPanel(closes []float64, date string) *model.IndicatorPanel {
	p := &model.IndicatorPanel{
		Date:        date,
		Cycle:       map[string]model.Indicator{},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	}
	if len(closes) < 200 {
		// 数据不足，标全 Missing 让 verdict 引擎诚实输出"覆盖率不足"
		markAllMissing(p)
		return p
	}
	cur := closes[len(closes)-1]
	p.Snapshot = model.SnapshotData{BTCPrice: cur, DataDate: date}

	// SMA 200d (Mayer 用)
	sma200d := mean(closes[len(closes)-200:])
	mayer := cur / sma200d
	p.Cycle["mayer_multiple"] = model.Indicator{Value: mayer, Source: "binance"}

	// SMA 200w (= 1400d)
	if len(closes) >= 1400 {
		sma200w := mean(closes[len(closes)-1400:])
		dev := (cur/sma200w - 1) * 100
		p.Cycle["sma_200w_dev"] = model.Indicator{Value: dev, Source: "binance"}
	} else {
		p.Cycle["sma_200w_dev"] = model.Indicator{Missing: true, Source: "kline 历史不足 1400d"}
	}

	// Pi Cycle Top: 111dMA / (2 × 350dMA)
	if len(closes) >= 350 {
		ma111 := mean(closes[len(closes)-111:])
		ma350 := mean(closes[len(closes)-350:])
		pi := ma111 / (2 * ma350)
		p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Value: pi, Source: "binance"}
	} else {
		p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Missing: true, Source: "kline 历史不足 350d"}
	}

	// 简化 AHR999：用 200d 调和均值代替 DCA + 长期拟合
	// 这只是 backtest 的代理；生产 AHR 用 calculator 完整版
	ahr := mayer * mayer / 1.5 // crude shape — proxy
	p.Valuation["ahr999"] = model.Indicator{Value: ahr, Source: "kline:proxy", Note: "回测简化版，非生产 AHR"}

	// RSI(14)
	if len(closes) >= 15 {
		p.Technical["rsi_14"] = model.Indicator{Value: rsi(closes, 14), Source: "binance"}
	} else {
		p.Technical["rsi_14"] = model.Indicator{Missing: true}
	}

	// MA alignment (50 vs 200)
	if len(closes) >= 200 {
		ma50 := mean(closes[len(closes)-50:])
		ma200 := mean(closes[len(closes)-200:])
		p.Technical["ma_alignment"] = model.Indicator{Value: ma50 - ma200, Source: "binance"}
	}

	// MACD histogram (12,26,9)
	if len(closes) >= 35 {
		hist := macdHistogram(closes, 12, 26, 9)
		p.Technical["macd_histogram"] = model.Indicator{Value: hist, Source: "binance"}
	}

	// 把 backtest 不可用的指标显式标 Missing，让 coverage 诚实反映
	for _, k := range []string{"funding_rate_pct", "oi_to_mc", "fear_greed", "skew_25d_pct", "dvol"} {
		p.Positioning[k] = model.Indicator{Missing: true, Source: "backtest:not_available"}
	}
	for _, k := range []string{"hash_ribbons", "difficulty_change_pct"} {
		p.Network[k] = model.Indicator{Missing: true, Source: "backtest:not_available"}
	}
	for _, k := range []string{"m2_yoy", "real_yield_10y_pct", "dxy_60d_trend_pct"} {
		p.Macro[k] = model.Indicator{Missing: true, Source: "backtest:not_available"}
	}
	for _, k := range []string{"etf_net_flow_30d_usd", "stablecoin_supply_30d_pct"} {
		p.Flow[k] = model.Indicator{Missing: true, Source: "backtest:not_available"}
	}
	for _, k := range []string{"mvrv_z_score", "nupl", "price_to_realized_dev_pct"} {
		p.Valuation[k] = model.Indicator{Missing: true, Source: "backtest:not_available"}
	}
	for _, k := range []string{"btc_spy_corr_30d", "rel_strength_90d_gold"} {
		p.CrossAsset[k] = model.Indicator{Missing: true, Source: "backtest:not_available"}
	}

	return p
}

func markAllMissing(p *model.IndicatorPanel) {
	for _, k := range []string{"mayer_multiple", "sma_200w_dev", "pi_cycle_top_ratio"} {
		p.Cycle[k] = model.Indicator{Missing: true}
	}
}

// --- 数学辅助 ---

func mean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func rsi(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 50
	}
	gains, losses := 0.0, 0.0
	tail := closes[len(closes)-period-1:]
	for i := 1; i < len(tail); i++ {
		d := tail[i] - tail[i-1]
		if d > 0 {
			gains += d
		} else {
			losses -= d
		}
	}
	if losses == 0 {
		return 100
	}
	rs := gains / losses
	return 100 - 100/(1+rs)
}

func emaSeries(closes []float64, period int) []float64 {
	if len(closes) == 0 {
		return nil
	}
	k := 2.0 / float64(period+1)
	out := make([]float64, len(closes))
	out[0] = closes[0]
	for i := 1; i < len(closes); i++ {
		out[i] = closes[i]*k + out[i-1]*(1-k)
	}
	return out
}

func macdHistogram(closes []float64, fast, slow, signal int) float64 {
	if len(closes) < slow+signal {
		return 0
	}
	emaFast := emaSeries(closes, fast)
	emaSlow := emaSeries(closes, slow)
	macd := make([]float64, len(closes))
	for i := range macd {
		macd[i] = emaFast[i] - emaSlow[i]
	}
	sig := emaSeries(macd, signal)
	last := len(closes) - 1
	return macd[last] - sig[last]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- 报告打印 ---

func printReport(r *engine.BacktestReport) {
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════")
	fmt.Printf("  guanfu-backtest 报告  %s → %s\n", r.From, r.To)
	fmt.Println("════════════════════════════════════════════════════════════")
	fmt.Printf("  采样数：%d   平均覆盖率：%.0f%%\n", r.NumSamples, r.AvgCoverage*100)
	fmt.Println()
	fmt.Println("  按 Stance 聚合（hit rate = 方向正确率）：")
	fmt.Printf("  %-22s %5s  %8s %8s %8s   %8s %8s %8s\n",
		"Stance", "N", "fwd30", "fwd90", "fwd180", "hit30", "hit90", "hit180")
	fmt.Println("  " + repeat("-", 92))
	for _, s := range r.StanceStats {
		hit30 := "n/a"
		hit90 := "n/a"
		hit180 := "n/a"
		if !math.IsNaN(s.HitRate30) && s.HitRate30 != 0 {
			hit30 = fmt.Sprintf("%.0f%%", s.HitRate30*100)
		}
		if !math.IsNaN(s.HitRate90) && s.HitRate90 != 0 {
			hit90 = fmt.Sprintf("%.0f%%", s.HitRate90*100)
		}
		if !math.IsNaN(s.HitRate180) && s.HitRate180 != 0 {
			hit180 = fmt.Sprintf("%.0f%%", s.HitRate180*100)
		}
		fmt.Printf("  %-22s %5d  %+7.1f%% %+7.1f%% %+7.1f%%    %8s %8s %8s\n",
			s.Stance, s.N, s.AvgFwd30, s.AvgFwd90, s.AvgFwd180, hit30, hit90, hit180)
	}
	fmt.Println()
	fmt.Println("  按 BottomProximity 分桶（验证底部接近度）：")
	fmt.Printf("  %-30s %5s  %10s %10s %10s\n", "Bucket", "N", "fwd30", "fwd90", "fwd180")
	fmt.Println("  " + repeat("-", 75))
	for _, s := range r.BottomProximity {
		fmt.Printf("  %-30s %5d  %+9.1f%% %+9.1f%% %+9.1f%%\n",
			s.Bucket, s.N, s.AvgFwd30, s.AvgFwd90, s.AvgFwd180)
	}
	fmt.Println()
	fmt.Println("  按 TopProximity 分桶（验证顶部接近度）：")
	fmt.Printf("  %-30s %5s  %10s %10s %10s\n", "Bucket", "N", "fwd30", "fwd90", "fwd180")
	fmt.Println("  " + repeat("-", 75))
	for _, s := range r.TopProximity {
		fmt.Printf("  %-30s %5d  %+9.1f%% %+9.1f%% %+9.1f%%\n",
			s.Bucket, s.N, s.AvgFwd30, s.AvgFwd90, s.AvgFwd180)
	}
	fmt.Println()
	fmt.Println("  期望：BottomProximity > 0.7 → fwd 收益高于 < 0.3；TopProximity 反之。")
	fmt.Println("════════════════════════════════════════════════════════════")
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// 静默 unused warnings (os 用于 import 完整性)
var _ = os.Args
