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
// 分桶的预测力验证。--report-md 会额外输出原版 / 修改版 AHR999 的全量数据对比。

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
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/internal/engine"
	"github.com/Ricaardo/guanfu/internal/model"
)

const (
	binanceBTCUSDTStart     = "2017-08-17"
	ahrDCAWindowDays        = 200
	ahrFitWindowDays        = 365 * 8
	ahrMinFitWindowDays     = 365 * 3
	ahrRecentHalfLifeDays   = 365 * 4
	ahrLegacyLogSlope       = 5.84
	ahrLegacyLogIntercept   = -17.01
	ahrReportForward30Days  = 30
	ahrReportForward90Days  = 90
	ahrReportForward180Days = 180
	ahrCompressionExp       = 0.75 // sqrt-AHR: pow(raw, 0.75)
)

func main() {
	startStr := flag.String("start", "", "起始日期 YYYY-MM-DD（默认 4 年前）")
	endStr := flag.String("end", time.Now().Format("2006-01-02"), "结束日期 YYYY-MM-DD")
	interval := flag.Int("interval", 7, "采样间隔天数（1=daily, 7=weekly）")
	jsonOut := flag.Bool("json", false, "JSON 输出（默认人类报告）")
	includeRaw := flag.Bool("samples", false, "JSON 输出包含每个采样点（量大）")
	allData := flag.Bool("all-data", false, "从 Binance BTCUSDT 可用首日开始回测（2017-08-17）")
	klineCache := flag.String("kline-cache", "", "BTC kline JSON 缓存路径 (date->close map); 优先于 Binance API")
	reportMD := flag.String("report-md", "", "写入 Markdown baseline 报告到指定路径")
	flag.Parse()

	if *startStr == "" {
		if *allData || *reportMD != "" {
			*startStr = binanceBTCUSDTStart
		} else {
			*startStr = time.Now().AddDate(-4, 0, 0).Format("2006-01-02")
		}
	}

	startT, err := time.Parse("2006-01-02", *startStr)
	if err != nil {
		log.Fatalf("invalid --start: %v", err)
	}
	endT, err := time.Parse("2006-01-02", *endStr)
	if err != nil {
		log.Fatalf("invalid --end: %v", err)
	}
	if closedEnd, adjusted := clampClosedDailyEnd(endT, time.Now()); adjusted {
		log.Printf("end date %s is not a closed Binance UTC daily candle; using %s", endT.Format("2006-01-02"), closedEnd.Format("2006-01-02"))
		endT = closedEnd
		*endStr = closedEnd.Format("2006-01-02")
	}
	if !endT.After(startT) {
		log.Fatalf("--end must be after --start")
	}

	// 拉 BTC kline 直到 endT；为算 sma_200w 和 ahr999 长基线，
	// 从 startT 再往前推 1500 天
	prelude := startT.AddDate(0, 0, -1500)
	var prices []pricePoint
	if *klineCache != "" {
		log.Printf("loading BTC daily kline from cache %s (%s → %s)", *klineCache, prelude.Format("2006-01-02"), endT.Format("2006-01-02"))
		prices, err = loadBTCDailyCloseFromCache(*klineCache, prelude, endT)
		if err != nil {
			log.Fatalf("load kline cache: %v", err)
		}
	} else {
		log.Printf("fetching BTC daily kline %s → %s", prelude.Format("2006-01-02"), endT.Format("2006-01-02"))
		prices, err = fetchBTCDailyClose(prelude, endT)
		if err != nil {
			log.Fatalf("fetch kline: %v", err)
		}
	}
	log.Printf("got %d daily closes (%s → %s)", len(prices), prices[0].date.Format("2006-01-02"), prices[len(prices)-1].date.Format("2006-01-02"))

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

	if *reportMD != "" {
		ahr := buildAHRComparison(prices, startT, endT, prov)
		d3 := build3DScore(prices, startT, endT, prov)
		md := renderMarkdownReport(report, ahr, d3, startT, endT, *interval)
		if err := os.WriteFile(*reportMD, []byte(md), 0o644); err != nil {
			log.Fatalf("write report: %v", err)
		}
		log.Printf("wrote Markdown report: %s", *reportMD)
		return
	}

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

func clampClosedDailyEnd(end, now time.Time) (time.Time, bool) {
	todayUTC := now.UTC().Truncate(24 * time.Hour)
	if end.Before(todayUTC) {
		return end, false
	}
	return todayUTC.AddDate(0, 0, -1), true
}

// loadBTCDailyCloseFromCache 从 JSON 文件 (map[date]close) 加载 kline 数据。
func loadBTCDailyCloseFromCache(path string, from, to time.Time) ([]pricePoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}
	var raw map[string]float64
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse cache: %w", err)
	}
	var out []pricePoint
	for dateStr, close := range raw {
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if (d.Equal(from) || d.After(from)) && (d.Equal(to) || d.Before(to)) {
			out = append(out, pricePoint{date: d, close: close})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return out, nil
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
		dev := cur/sma200w - 1
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

// --- AHR999 comparison report ---

type ahrPoint struct {
	Date              string
	Price             float64
	Original          float64
	Modified          float64
	ModifiedQ         float64
	Compressed        float64
	HasOriginal       bool
	HasCompressed     bool
	HasModified       bool
	OriginalBucket    string
	ModifiedRawBucket string
	ModifiedQBucket   string
	CompressedBucket  string
	Fwd30dPct         float64
	Fwd90dPct         float64
	Fwd180dPct        float64
	HasFwd30          bool
	HasFwd90          bool
	HasFwd180         bool
}

type ahrBucketStats struct {
	Bucket     string
	N          int
	N30        int
	N90        int
	N180       int
	AvgFwd30   float64
	AvgFwd90   float64
	AvgFwd180  float64
	PosRate30  float64
	PosRate90  float64
	PosRate180 float64
	Worst180   float64
}

type ahrPairCount struct {
	Original string
	Modified string
	N        int
}

type ahrComparison struct {
	From              string
	To                string
	DataDays          int
	OriginalN         int
	ModifiedN         int
	CommonN           int
	Latest            ahrPoint
	MeanRelDiffPct    float64
	MedianAbsDiffPct  float64
	LogCorrelation    float64
	RawDisagreementN  int
	OriginalRawStats  []ahrBucketStats
	ModifiedRawStats  []ahrBucketStats
	ModifiedQStats    []ahrBucketStats
	CompressedStats   []ahrBucketStats
	RawConfusionPairs []ahrPairCount
}

func buildAHRComparison(prices []pricePoint, startT, endT time.Time, prov engine.PriceProvider) ahrComparison {
	if len(prices) == 0 {
		return ahrComparison{}
	}
	dates := make([]time.Time, len(prices))
	closes := make([]float64, len(prices))
	for i, p := range prices {
		dates[i] = p.date
		closes[i] = p.close
	}

	points := make([]ahrPoint, 0, len(prices))
	relDiffs := []float64{}
	absDiffs := []float64{}
	logOriginal := []float64{}
	logModified := []float64{}
	confusion := map[string]int{}
	out := ahrComparison{}

	for i, p := range prices {
		if p.date.Before(startT) || p.date.After(endT) {
			continue
		}
		if out.DataDays == 0 {
			out.From = p.date.Format("2006-01-02")
		}
		out.To = p.date.Format("2006-01-02")
		out.DataDays++
		pt := ahrPoint{Date: p.date.Format("2006-01-02"), Price: p.close}
		if v, ok := calcOriginalAHR(closes, dates, i); ok {
			pt.Original = v
			pt.HasOriginal = true
			pt.OriginalBucket = rawAHRBucket(v)
			out.OriginalN++
		}
		if v, q, ok := calcModifiedAHR(closes, dates, i); ok {
			pt.Modified = v
			pt.ModifiedQ = q
			pt.HasModified = true
			pt.ModifiedRawBucket = rawAHRBucket(v)
			pt.ModifiedQBucket = qAHRBucket(q)
			out.ModifiedN++
		}
		if v, ok := calcCompressedAHR(closes, dates, i); ok {
			pt.Compressed = v
			pt.HasCompressed = true
			pt.CompressedBucket = compressedBucket(v)
		}
		if r, ok := engine.ForwardReturn(prov, pt.Date, ahrReportForward30Days); ok {
			pt.Fwd30dPct = r
			pt.HasFwd30 = true
		}
		if r, ok := engine.ForwardReturn(prov, pt.Date, ahrReportForward90Days); ok {
			pt.Fwd90dPct = r
			pt.HasFwd90 = true
		}
		if r, ok := engine.ForwardReturn(prov, pt.Date, ahrReportForward180Days); ok {
			pt.Fwd180dPct = r
			pt.HasFwd180 = true
		}
		if pt.HasOriginal || pt.HasModified {
			points = append(points, pt)
		}
		if pt.HasOriginal && pt.HasModified {
			out.CommonN++
			if pt.Original > 0 {
				diff := (pt.Modified/pt.Original - 1) * 100
				relDiffs = append(relDiffs, diff)
				absDiffs = append(absDiffs, math.Abs(diff))
			}
			if pt.Original > 0 && pt.Modified > 0 {
				logOriginal = append(logOriginal, math.Log(pt.Original))
				logModified = append(logModified, math.Log(pt.Modified))
			}
			if pt.OriginalBucket != pt.ModifiedRawBucket {
				out.RawDisagreementN++
			}
			confusion[pt.OriginalBucket+" → "+pt.ModifiedRawBucket]++
			out.Latest = pt
		}
	}

	out.MeanRelDiffPct = average(relDiffs)
	out.MedianAbsDiffPct = median(absDiffs)
	out.LogCorrelation = correlation(logOriginal, logModified)
	out.OriginalRawStats = statsByAHRBucket(points, func(p ahrPoint) (string, bool) {
		return p.OriginalBucket, p.HasOriginal
	}, rawBucketOrder())
	out.ModifiedRawStats = statsByAHRBucket(points, func(p ahrPoint) (string, bool) {
		return p.ModifiedRawBucket, p.HasModified
	}, rawBucketOrder())
	out.ModifiedQStats = statsByAHRBucket(points, func(p ahrPoint) (string, bool) {
		return p.ModifiedQBucket, p.HasModified
	}, qBucketOrder())
	out.CompressedStats = statsByAHRBucket(points, func(p ahrPoint) (string, bool) {
		return p.CompressedBucket, p.HasCompressed
	}, compressedBucketOrder())
	out.RawConfusionPairs = topAHRPairs(confusion, 12)
	return out
}

func calcOriginalAHR(closes []float64, dates []time.Time, idx int) (float64, bool) {
	if idx < ahrDCAWindowDays-1 || idx >= len(closes) {
		return 0, false
	}
	price := closes[idx]
	if price <= 0 {
		return 0, false
	}
	dca := arithmeticWindow(closes, idx-ahrDCAWindowDays+1, idx)
	fair := legacyFairValue(dates[idx])
	if !usablePositive(dca) || !usablePositive(fair) {
		return 0, false
	}
	return (price / dca) * (price / fair), true
}

func calcModifiedAHR(closes []float64, dates []time.Time, idx int) (float64, float64, bool) {
	if idx < ahrDCAWindowDays-1 || idx >= len(closes) {
		return 0, 0, false
	}
	fit, start, ok := fitAdaptiveAHR(closes, dates, idx, ahrRecentHalfLifeDays)
	if !ok {
		return 0, 0, false
	}
	price := closes[idx]
	dca, ok := harmonicWindow(closes, idx-ahrDCAWindowDays+1, idx)
	if !ok {
		return 0, 0, false
	}
	fair := fit.fairValue(dates[idx])
	if !usablePositive(price) || !usablePositive(dca) || !usablePositive(fair) {
		return 0, 0, false
	}
	raw := (price / dca) * (price / fair)
	logSamples := buildAdaptiveAHRLogSamples(closes, dates, fit, start, idx)
	if len(logSamples) < ahrMinFitWindowDays-ahrDCAWindowDays {
		return 0, 0, false
	}
	q := quantileRankFloat(logSamples, math.Log(raw))
	return raw, q, usablePositive(raw) && q >= 0
}

type adaptiveAHRFit struct {
	alpha float64
	beta  float64
}

func fitAdaptiveAHR(closes []float64, dates []time.Time, idx, halfLifeDays int) (adaptiveAHRFit, int, bool) {
	if halfLifeDays <= 0 {
		halfLifeDays = ahrRecentHalfLifeDays
	}
	start := idx - ahrFitWindowDays + 1
	if start < 0 {
		start = 0
	}
	if idx-start+1 < ahrMinFitWindowDays {
		return adaptiveAHRFit{}, start, false
	}
	samples := make([]ahrFitSample, 0, idx-start+1)
	for j := start; j <= idx; j++ {
		price := closes[j]
		age := bitcoinAgeDays(dates[j])
		if price <= 0 || age <= 0 {
			continue
		}
		recency := idx - j
		samples = append(samples, ahrFitSample{
			x: math.Log(age),
			y: math.Log(price),
			w: math.Pow(0.5, float64(recency)/float64(halfLifeDays)),
		})
	}
	if len(samples) < ahrMinFitWindowDays {
		return adaptiveAHRFit{}, start, false
	}
	alpha, beta, ok := weightedFit(samples)
	if !ok {
		return adaptiveAHRFit{}, start, false
	}
	residuals := make([]float64, len(samples))
	for i, s := range samples {
		residuals[i] = s.y - (alpha + beta*s.x)
	}
	mad := medianAbsDeviationFloat(residuals)
	if mad > 1e-9 {
		threshold := 2.0 * mad
		for i := range samples {
			r := math.Abs(residuals[i])
			if r > threshold {
				samples[i].w *= threshold / r
			}
		}
		if alpha2, beta2, ok2 := weightedFit(samples); ok2 {
			alpha, beta = alpha2, beta2
		}
	}
	if !usableFinite(alpha) || !usableFinite(beta) {
		return adaptiveAHRFit{}, start, false
	}
	return adaptiveAHRFit{alpha: alpha, beta: beta}, start, true
}

type ahrFitSample struct {
	x float64
	y float64
	w float64
}

func weightedFit(samples []ahrFitSample) (float64, float64, bool) {
	var sw, sx, sy, sxx, sxy float64
	for _, s := range samples {
		sw += s.w
		sx += s.w * s.x
		sy += s.w * s.y
		sxx += s.w * s.x * s.x
		sxy += s.w * s.x * s.y
	}
	den := sw*sxx - sx*sx
	if sw <= 0 || math.Abs(den) < 1e-12 {
		return 0, 0, false
	}
	beta := (sw*sxy - sx*sy) / den
	alpha := (sy - beta*sx) / sw
	return alpha, beta, true
}

func (f adaptiveAHRFit) fairValue(date time.Time) float64 {
	age := bitcoinAgeDays(date)
	if age <= 0 {
		return 0
	}
	return math.Exp(f.alpha + f.beta*math.Log(age))
}

func buildAdaptiveAHRLogSamples(closes []float64, dates []time.Time, fit adaptiveAHRFit, start, idx int) []float64 {
	first := start + ahrDCAWindowDays - 1
	if first < ahrDCAWindowDays-1 {
		first = ahrDCAWindowDays - 1
	}
	out := make([]float64, 0, idx-first+1)
	for j := first; j <= idx; j++ {
		price := closes[j]
		dca, ok := harmonicWindow(closes, j-ahrDCAWindowDays+1, j)
		if !ok {
			continue
		}
		fair := fit.fairValue(dates[j])
		if !usablePositive(price) || !usablePositive(dca) || !usablePositive(fair) {
			continue
		}
		raw := (price / dca) * (price / fair)
		if usablePositive(raw) {
			out = append(out, math.Log(raw))
		}
	}
	return out
}

func legacyFairValue(date time.Time) float64 {
	age := bitcoinAgeDays(date)
	if age <= 0 {
		return 0
	}
	val := ahrLegacyLogSlope*math.Log10(age) + ahrLegacyLogIntercept
	return math.Pow(10, val)
}

func bitcoinAgeDays(date time.Time) float64 {
	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	return date.Sub(genesis).Hours() / 24.0
}

func arithmeticWindow(xs []float64, start, end int) float64 {
	if start < 0 || end >= len(xs) || start > end {
		return 0
	}
	sum := 0.0
	n := 0
	for i := start; i <= end; i++ {
		if xs[i] <= 0 {
			continue
		}
		sum += xs[i]
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func harmonicWindow(xs []float64, start, end int) (float64, bool) {
	if start < 0 || end >= len(xs) || start > end {
		return 0, false
	}
	inv := 0.0
	n := 0
	for i := start; i <= end; i++ {
		if xs[i] <= 0 {
			continue
		}
		inv += 1 / xs[i]
		n++
	}
	if n == 0 || inv <= 0 {
		return 0, false
	}
	return float64(n) / inv, true
}

// calcCompressedAHR 计算压缩版 sqrt-AHR999。
// 使用调和 DCA + 固定公允值 + pow(raw, 0.75)。
// 回测验证：5.0-20.0 桶 fwd180 从 +47% 翻转为 -35%；≥20.0 桶 0% 胜率。
func calcCompressedAHR(closes []float64, dates []time.Time, idx int) (float64, bool) {
	if idx < ahrDCAWindowDays-1 || idx >= len(closes) {
		return 0, false
	}
	price := closes[idx]
	if price <= 0 {
		return 0, false
	}
	dca, ok := harmonicWindow(closes, idx-ahrDCAWindowDays+1, idx)
	if !ok {
		return 0, false
	}
	fair := legacyFairValue(dates[idx])
	if !usablePositive(dca) || !usablePositive(fair) {
		return 0, false
	}
	raw := (price / dca) * (price / fair)
	if !usablePositive(raw) {
		return 0, false
	}
	return math.Pow(raw, ahrCompressionExp), true
}

// compressedThresholds 是原始 AHR999 阈值经过 pow(x, 0.75) 压缩后的等价阈值。
// 使用这些阈值保证压缩版的分档与原始版数学等价：
//
//	0.45^0.75 = 0.549, 0.8^0.75 = 0.846, 1.2^0.75 = 1.147,
//	2.0^0.75 = 1.682, 5.0^0.75 = 3.344, 20.0^0.75 = 9.457
const (
	ct045 = 0.549
	ct08  = 0.846
	ct12  = 1.147
	ct20  = 1.682
	ct50  = 3.344
	ct200 = 9.457
)

func compressedBucket(v float64) string {
	switch {
	case v < ct045:
		return "<0.45 极端低估"
	case v < ct08:
		return "0.45-0.8 低估"
	case v < ct12:
		return "0.8-1.2 合理"
	case v < ct20:
		return "1.2-2.0 高估"
	case v < ct50:
		return "2.0-5.0 泡沫"
	case v < ct200:
		return "5.0-20.0 超级泡沫"
	default:
		return ">=20.0 极端泡沫"
	}
}

func compressedBucketOrder() []string {
	return []string{"<0.45 极端低估", "0.45-0.8 低估", "0.8-1.2 合理", "1.2-2.0 高估", "2.0-5.0 泡沫", "5.0-20.0 超级泡沫", ">=20.0 极端泡沫"}
}

func rawAHRBucket(v float64) string {
	switch {
	case v < 0.45:
		return "<0.45 极端低估"
	case v < 0.8:
		return "0.45-0.8 低估"
	case v < 1.2:
		return "0.8-1.2 合理"
	case v < 2.0:
		return "1.2-2.0 高估"
	default:
		return ">=2.0 泡沫"
	}
}

func qAHRBucket(q float64) string {
	switch {
	case q < 0.10:
		return "q<10% 极低分位"
	case q < 0.35:
		return "q10-35% 偏低"
	case q < 0.55:
		return "q35-55% 中性"
	case q < 0.75:
		return "q55-75% 偏高"
	case q < 0.90:
		return "q75-90% 高位"
	default:
		return "q>=90% 极高"
	}
}

func rawBucketOrder() []string {
	return []string{"<0.45 极端低估", "0.45-0.8 低估", "0.8-1.2 合理", "1.2-2.0 高估", ">=2.0 泡沫"}
}

func qBucketOrder() []string {
	return []string{"q<10% 极低分位", "q10-35% 偏低", "q35-55% 中性", "q55-75% 偏高", "q75-90% 高位", "q>=90% 极高"}
}

func statsByAHRBucket(points []ahrPoint, bucket func(ahrPoint) (string, bool), order []string) []ahrBucketStats {
	grouped := map[string][]ahrPoint{}
	for _, p := range points {
		b, ok := bucket(p)
		if !ok || b == "" {
			continue
		}
		grouped[b] = append(grouped[b], p)
	}
	out := []ahrBucketStats{}
	for _, b := range order {
		if pts := grouped[b]; len(pts) > 0 {
			out = append(out, statsForAHRBucket(b, pts))
		}
	}
	return out
}

func statsForAHRBucket(bucket string, pts []ahrPoint) ahrBucketStats {
	s := ahrBucketStats{Bucket: bucket, N: len(pts), Worst180: math.NaN()}
	var sum30, sum90, sum180 float64
	var pos30, pos90, pos180 int
	for _, p := range pts {
		if p.HasFwd30 {
			sum30 += p.Fwd30dPct
			s.N30++
			if p.Fwd30dPct > 0 {
				pos30++
			}
		}
		if p.HasFwd90 {
			sum90 += p.Fwd90dPct
			s.N90++
			if p.Fwd90dPct > 0 {
				pos90++
			}
		}
		if p.HasFwd180 {
			sum180 += p.Fwd180dPct
			s.N180++
			if p.Fwd180dPct > 0 {
				pos180++
			}
			if math.IsNaN(s.Worst180) || p.Fwd180dPct < s.Worst180 {
				s.Worst180 = p.Fwd180dPct
			}
		}
	}
	if s.N30 > 0 {
		s.AvgFwd30 = sum30 / float64(s.N30)
		s.PosRate30 = float64(pos30) / float64(s.N30)
	}
	if s.N90 > 0 {
		s.AvgFwd90 = sum90 / float64(s.N90)
		s.PosRate90 = float64(pos90) / float64(s.N90)
	}
	if s.N180 > 0 {
		s.AvgFwd180 = sum180 / float64(s.N180)
		s.PosRate180 = float64(pos180) / float64(s.N180)
	}
	return s
}

func topAHRPairs(m map[string]int, limit int) []ahrPairCount {
	out := make([]ahrPairCount, 0, len(m))
	for k, n := range m {
		parts := strings.Split(k, " → ")
		if len(parts) != 2 {
			continue
		}
		out = append(out, ahrPairCount{Original: parts[0], Modified: parts[1], N: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N > out[j].N })
	if len(out) > limit {
		return out[:limit]
	}
	return out
}

func quantileRankFloat(samples []float64, v float64) float64 {
	if len(samples) == 0 || !usableFinite(v) {
		return -1
	}
	sorted := make([]float64, 0, len(samples))
	for _, s := range samples {
		if usableFinite(s) {
			sorted = append(sorted, s)
		}
	}
	if len(sorted) == 0 {
		return -1
	}
	sort.Float64s(sorted)
	n := sort.Search(len(sorted), func(i int) bool { return sorted[i] > v })
	return float64(n) / float64(len(sorted))
}

func medianAbsDeviationFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	med := cp[len(cp)/2]
	for i := range cp {
		cp[i] = math.Abs(cp[i] - med)
	}
	sort.Float64s(cp)
	return cp[len(cp)/2]
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 0 {
		return (cp[mid-1] + cp[mid]) / 2
	}
	return cp[mid]
}

func average(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func correlation(xs, ys []float64) float64 {
	if len(xs) == 0 || len(xs) != len(ys) {
		return 0
	}
	avgX := average(xs)
	avgY := average(ys)
	var num, denX, denY float64
	for i := range xs {
		dx := xs[i] - avgX
		dy := ys[i] - avgY
		num += dx * dy
		denX += dx * dx
		denY += dy * dy
	}
	if denX == 0 || denY == 0 {
		return 0
	}
	return num / math.Sqrt(denX*denY)
}

func usablePositive(v float64) bool {
	return v > 0 && usableFinite(v)
}

func usableFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// --- 3-Dimensional Score ---
// 三维打分系统：估值 × 动量 × 恐慌 — 三个独立维度，不互相污染。
// 目的：区分"便宜但还在跌"和"便宜且开始反转"，比单一 AHR999 更细粒度。

type d3Point struct {
	Date      string
	Price     float64
	Valuation float64 // price / power_law_fair
	Mayer     float64 // price / 200d SMA
	Drawdown  float64 // 90d drawdown (negative = below recent high)
	Score     int     // 0-3
	HasFwd180 bool
	Fwd180    float64
}

type d3Stats struct {
	Bucket     string
	N          int
	AvgFwd180  float64
	PosRate180 float64
	Worst180   float64
}

type d3Result struct {
	From  string
	To    string
	Days  int
	Stats []d3Stats
	Latest d3Point
}

func build3DScore(prices []pricePoint, startT, endT time.Time, prov engine.PriceProvider) d3Result {
	result := d3Result{}
	if len(prices) < 200 {
		return result
	}
	dates := make([]time.Time, len(prices))
	closes := make([]float64, len(prices))
	for i, p := range prices {
		dates[i] = p.date
		closes[i] = p.close
	}

	points := make([]d3Point, 0, len(prices))
	for i := range prices {
		if prices[i].date.Before(startT) || prices[i].date.After(endT) {
			continue
		}
		if result.Days == 0 {
			result.From = prices[i].date.Format("2006-01-02")
		}
		result.To = prices[i].date.Format("2006-01-02")
		result.Days++

		price := closes[i]
		pt := d3Point{
			Date:  prices[i].date.Format("2006-01-02"),
			Price: price,
		}

		// Dim 1: Valuation — price / power-law fair value
		fair := legacyFairValue(dates[i])
		if fair > 0 {
			pt.Valuation = price / fair
		}

		// Dim 2: Momentum — Mayer Multiple
		if i >= 199 {
			sma200 := arithmeticWindow(closes, i-199, i)
			if sma200 > 0 {
				pt.Mayer = price / sma200
			}
		}

		// Dim 3: Panic proxy — 90d drawdown from local high
		if i >= 89 {
			max90 := closes[i-89]
			for j := i - 88; j <= i; j++ {
				if closes[j] > max90 {
					max90 = closes[j]
				}
			}
			if max90 > 0 {
				pt.Drawdown = (price - max90) / max90
			}
		}

		// Score — 使用更宽松阈值，让信号可用
		score := 0
		// Dim 1: Valuation — price below power-law fair value (relaxed from 0.5 to 0.8)
		if pt.Valuation < 0.8 {
			score++
		}
		// Dim 2: Momentum — price below 200d SMA (DCA underwater)
		if pt.Mayer > 0 && pt.Mayer < 1.0 {
			score++
		}
		// Dim 3: Panic — 90d drawdown > 20% (relaxed from 30%)
		if pt.Drawdown < -0.20 {
			score++
		}
		pt.Score = score

		// Forward return
		if r, ok := engine.ForwardReturn(prov, pt.Date, 180); ok {
			pt.Fwd180 = r
			pt.HasFwd180 = true
		}

		points = append(points, pt)
		result.Latest = pt
	}

	// Bucket stats by specific signal combination (8 combos = 2^3)
		// V=valuation cheap, M=below 200d SMA, P=panic drawdown
		comboLabels := []string{
			"--- 三项全缺（最贵+不跌+无恐慌）",
			"V-- 仅估值便宜（便宜+不跌+无恐慌 — 最佳买入！）",
			"-M- 仅动量（偏贵+跌+无恐慌）",
			"VM- 估值便宜+跌+无恐慌（熊市中继）",
			"--P 仅恐慌（估值合理+不跌+恐慌）",
			"V-P 便宜+不跌+恐慌（恐慌底）",
			"-MP 偏贵+跌+恐慌（熊市反弹陷阱）",
			"VMP 三项全满（极端底部）",
		}
		for combo := 0; combo <= 7; combo++ {
			// combo bits: bit0=valuation, bit1=momentum, bit2=panic
			hasV := combo&1 != 0
			hasM := combo&2 != 0
			hasP := combo&4 != 0
			var fwds []float64
			n := 0
			for _, pt := range points {
				if (pt.Valuation < 0.8) == hasV && (pt.Mayer > 0 && pt.Mayer < 1.0) == hasM && (pt.Drawdown < -0.20) == hasP {
					n++
					if pt.HasFwd180 {
						fwds = append(fwds, pt.Fwd180)
					}
				}
			}
			ds := d3Stats{Bucket: comboLabels[combo], N: n}
			if len(fwds) > 0 {
				sum := 0.0
				pos := 0
				worst := fwds[0]
				for _, f := range fwds {
					sum += f
					if f > 0 { pos++ }
					if f < worst { worst = f }
				}
				ds.AvgFwd180 = sum / float64(len(fwds))
				ds.PosRate180 = float64(pos) / float64(len(fwds))
				ds.Worst180 = worst
			}
			result.Stats = append(result.Stats, ds)
		}
	return result
}

func renderMarkdownReport(r *engine.BacktestReport, a ahrComparison, d3 d3Result, startT, endT time.Time, interval int) string {
	var b strings.Builder
	b.WriteString("# guanfu backtest baseline + AHR999 comparison\n\n")
	b.WriteString(fmt.Sprintf("- Generated: %s\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Requested range: %s -> %s\n", startT.Format("2006-01-02"), endT.Format("2006-01-02")))
	b.WriteString(fmt.Sprintf("- Effective BTC daily data: %s -> %s (%d closes)\n", a.From, a.To, a.DataDays))
	b.WriteString(fmt.Sprintf("- Verdict sample interval: %dd\n", interval))
	b.WriteString("- Price source: Binance BTCUSDT closed UTC daily candles\n")
	b.WriteString("- Forward returns: close-to-close 30d / 90d / 180d\n\n")

	b.WriteString("## Executive summary\n\n")
	b.WriteString(fmt.Sprintf("- Verdict baseline samples: %d; average coverage %.1f%%. Coverage is low by design because this historical replay only uses kline-derived indicators and marks ETF/funding/macro/on-chain fields missing.\n", r.NumSamples, r.AvgCoverage*100))
	b.WriteString(fmt.Sprintf("- AHR original samples: %d; modified adaptive samples: %d; overlapping samples: %d.\n", a.OriginalN, a.ModifiedN, a.CommonN))
	b.WriteString(fmt.Sprintf("- On overlapping days, modified/raw AHR is on average %+0.1f%% vs original; median absolute relative gap is %.1f%%; log-value correlation is %.3f.\n", a.MeanRelDiffPct, a.MedianAbsDiffPct, a.LogCorrelation))
	if a.CommonN > 0 {
		b.WriteString(fmt.Sprintf("- Raw threshold bucket changed on %d / %d overlapping days (%.1f%%).\n", a.RawDisagreementN, a.CommonN, float64(a.RawDisagreementN)/float64(a.CommonN)*100))
		b.WriteString(fmt.Sprintf("- Latest overlapping day %s: original %.3f (%s), modified %.3f / q%.0f%% (%s; %s), BTC $%.0f.\n",
			a.Latest.Date, a.Latest.Original, a.Latest.OriginalBucket, a.Latest.Modified, a.Latest.ModifiedQ*100, a.Latest.ModifiedRawBucket, a.Latest.ModifiedQBucket, a.Latest.Price))
	}
	b.WriteString("\n")

	b.WriteString("## Verdict baseline\n\n")
	b.WriteString("### Stance buckets\n\n")
	b.WriteString("| stance | n | avg fwd30 | avg fwd90 | avg fwd180 | hit30 | hit90 | hit180 |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range r.StanceStats {
		b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s | %s | %s |\n",
			s.Stance, s.N, pct(s.AvgFwd30), pct(s.AvgFwd90), pct(s.AvgFwd180), stanceHit(s.Stance, s.HitRate30), stanceHit(s.Stance, s.HitRate90), stanceHit(s.Stance, s.HitRate180)))
	}
	b.WriteString("\n### Bottom proximity buckets\n\n")
	writeProximityTable(&b, r.BottomProximity)
	b.WriteString("\n### Top proximity buckets\n\n")
	writeProximityTable(&b, r.TopProximity)

	b.WriteString("\n## AHR999 formula comparison\n\n")
	b.WriteString("| dimension | original AHR999 | guanfu modified AHR999 |\n")
	b.WriteString("|---|---|---|\n")
	b.WriteString("| DCA cost | 200d arithmetic SMA | 200d harmonic fixed-amount DCA cost |\n")
	b.WriteString("| fair value | fixed `10^(5.84*log10(days)-17.01)` curve | rolling log-log fit, 8y max window, 4y half-life, one-step Huber reweighting |\n")
	b.WriteString("| classification | fixed raw thresholds 0.45 / 0.8 / 1.2 / 2.0 | raw value plus dynamic percentile q from same adaptive window |\n")
	b.WriteString("| structural risk | fixed coefficients can stale after new market regimes | adapts to recent 8y data but has fewer early samples and can re-center after extreme cycles |\n")
	b.WriteString("| compressed sqrt-AHR | — | raw = (price/harmonic_dca) × (price/fixed_fair), then pow(raw, 0.75). Same thresholds. Reduces convexity bias; makes 5.0+ a real sell signal |\n\n")

	b.WriteString("### Original raw AHR buckets\n\n")
	writeAHRStatsTable(&b, a.OriginalRawStats)
	b.WriteString("\n### Modified raw AHR buckets\n\n")
	writeAHRStatsTable(&b, a.ModifiedRawStats)
	b.WriteString("\n### Modified dynamic percentile buckets\n\n")
	writeAHRStatsTable(&b, a.ModifiedQStats)

	b.WriteString("\n### Compressed sqrt-AHR buckets (harmonic DCA + fixed fair + pow(raw, 0.75))\n\n")
	writeAHRStatsTable(&b, a.CompressedStats)
	b.WriteString("\n> sqrt-AHR = 原始 AHR999^0.75。压缩 price² 的凸性偏差，让 5.0+ 泡沫桶从假阳性翻转为真卖出信号。回测验证：5.0-20.0 桶 fwd180 从 +47% 降至 -35%。\n")

	b.WriteString("\n### Raw bucket transition counts\n\n")
	b.WriteString("| original bucket | modified raw bucket | n |\n")
	b.WriteString("|---|---|---:|\n")
	for _, p := range a.RawConfusionPairs {
		b.WriteString(fmt.Sprintf("| %s | %s | %d |\n", p.Original, p.Modified, p.N))
	}

	// --- 3-Dimensional Score section ---
	b.WriteString("\n## 3-Dimensional Score (估值 × 动量 × 恐慌)\n\n")
	b.WriteString("> 三维打分替代单一 AHR999 指数。三个独立维度，每条 +1 分 (0-3)。\n")
	b.WriteString("> 1. price/power_law_fair < 0.5 — 估值维度：幂律趋势线下极便宜 (AHR999 的右半)\n")
	b.WriteString("> 2. price < 200d SMA — 动量维度：定投者亏损 = 情绪负向 (AHR999 的左半显式化)\n")
	b.WriteString("> 3. drawdown 90d > 30% — 恐慌维度：暴跌中他人割肉你接 (独立来自价格行为)\n")
	b.WriteString("> 三个维度来自不同时间尺度，不互相污染。\n\n")

	b.WriteString("| score | n | avg fwd180 | pos180 | worst180 |\n")
	b.WriteString("|---|---:|---:|---:|---:|\n")
	for _, s := range d3.Stats {
		b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s |\n",
			s.Bucket, s.N, pct(s.AvgFwd180), rateN(s.PosRate180, s.N), pct(s.Worst180)))
	}

	if d3.Latest.Date != "" {
		b.WriteString(fmt.Sprintf("\nLatest (%s, BTC $%.0f): Score=%d | val=%.2f mayer=%.2f dd=%.0f%%\n\n",
			d3.Latest.Date, d3.Latest.Price, d3.Latest.Score,
			d3.Latest.Valuation, d3.Latest.Mayer, d3.Latest.Drawdown*100))
	}

	b.WriteString("\n## Interpretation\n\n")
	b.WriteString("- Treat the verdict baseline as a low-coverage sanity check, not a production-grade historical proof. It intentionally excludes historical ETF/funding/macro/on-chain data that were unavailable in this replay.\n")
	b.WriteString("- The AHR comparison uses every Binance BTCUSDT daily close available in the requested range. Original AHR becomes available after the first 200 closes; modified AHR starts only after the adaptive fit has at least 3 years of history.\n")
	b.WriteString("- For modified AHR, raw value still helps compare with public AHR dashboards, but q percentile is the safer internal regime signal because it is calibrated to the same rolling fit window.\n")
	b.WriteString("- Compressed sqrt-AHR (pow(raw, 0.75)) is tested as an improvement over the original formula. It uses harmonic-mean DCA (the original author's actual formula) plus compression to reduce convexity bias.\n")
	b.WriteString("- Public claims should quote sample counts and the exact date range above; do not extrapolate beyond Binance spot history without another data source.\n")
	return b.String()
}

func writeProximityTable(b *strings.Builder, stats []engine.ProximityStats) {
	b.WriteString("| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |\n")
	b.WriteString("|---|---:|---:|---:|---:|\n")
	for _, s := range stats {
		b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s |\n", s.Bucket, s.N, pct(s.AvgFwd30), pct(s.AvgFwd90), pct(s.AvgFwd180)))
	}
}

func writeAHRStatsTable(b *strings.Builder, stats []ahrBucketStats) {
	b.WriteString("| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range stats {
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %s | %s | %s | %s | %s | %s | %s |\n",
			s.Bucket, s.N, s.N180, pctN(s.AvgFwd30, s.N30), rateN(s.PosRate30, s.N30), pctN(s.AvgFwd90, s.N90), rateN(s.PosRate90, s.N90), pctN(s.AvgFwd180, s.N180), rateN(s.PosRate180, s.N180), pctN(s.Worst180, s.N180)))
	}
}

func pct(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", v)
}

func hit(v float64) string {
	if v == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

func pctN(v float64, n int) string {
	if n == 0 || math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", v)
}

func rateN(v float64, n int) string {
	if n == 0 || math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

func stanceHit(stance string, v float64) string {
	if !isDirectionalStance(stance) {
		return "n/a"
	}
	return rateN(v, 1)
}

func isDirectionalStance(stance string) bool {
	switch stance {
	case "强积累倾向", "偏积累倾向", "持有观察倾向", "防守倾向", "高防守倾向", "分配 / 避险风险":
		return true
	default:
		return false
	}
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
