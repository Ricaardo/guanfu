// guanfu-threshold-search — 3D Score 阈值网格搜索
//
// 对 V(valuation)/M(momentum)/P(panic) 三个维度的阈值做网格搜索，
// 输出每档组合下 V-- 和 -M- 两个关键桶的 fwd180 平均回报和胜率。
// 帮助找到最优切分点。
//
// 用法：
//   go run ./cmd/guanfu-threshold-search/ --all-data
//   go run ./cmd/guanfu-threshold-search/ --start 2020-01-01 --end 2025-01-01 --plain

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
)

const binanceBTCUSDTStart = "2017-08-17"

type pricePoint struct {
	date  time.Time
	close float64
}

type d3Result struct {
	ThreshV     float64
	ThreshM     float64
	ThreshP     float64 // negative, e.g., -0.20 means 20% drawdown
	TotalDays   int
	VN          int
	VAvgFwd     float64
	VPosRate    float64
	MN          int
	MAvgFwd     float64
	MPosRate    float64
	VMPN        int
	VMPAvgFwd   float64
	VMPPosRate  float64
	NoSignalN   int
	NoSignalAvg float64
}

func main() {
	startStr := flag.String("start", "", "起始日期")
	endStr := flag.String("end", time.Now().Format("2006-01-02"), "结束日期")
	allData := flag.Bool("all-data", false, "全历史")
	jsonOut := flag.Bool("json", false, "JSON 输出")
	flag.Parse()

	if *startStr == "" {
		if *allData {
			*startStr = binanceBTCUSDTStart
		} else {
			*startStr = time.Now().AddDate(-4, 0, 0).Format("2006-01-02")
		}
	}

	startT, _ := time.Parse("2006-01-02", *startStr)
	endT, _ := time.Parse("2006-01-02", *endStr)
	if !endT.After(startT) {
		log.Fatal("--end must be after --start")
	}

	// 拉数据 + 1500d prelude
	prelude := startT.AddDate(0, 0, -1500)
	prices := fetchBTCDailyClose(prelude, endT)
	log.Printf("got %d daily closes", len(prices))

	closes := make([]float64, len(prices))
	dates := make([]time.Time, len(prices))
	for i, p := range prices {
		closes[i] = p.close
		dates[i] = p.date
	}

	// V/M/P 阈值网格
	vThresh := []float64{0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	mThresh := []float64{0.8, 0.9, 1.0, 1.1, 1.2}
	pThresh := []float64{-0.10, -0.15, -0.20, -0.25, -0.30, -0.40}

	var results []d3Result
	for _, vt := range vThresh {
		for _, mt := range mThresh {
			for _, pt := range pThresh {
				r := evalThresholds(closes, dates, startT, endT, vt, mt, pt)
				results = append(results, r)
			}
		}
	}

	if *jsonOut {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(b))
		return
	}
	printResults(results)
}

func evalThresholds(closes []float64, dates []time.Time, startT, endT time.Time, threshV, threshM, threshP float64) d3Result {
	r := d3Result{ThreshV: threshV, ThreshM: threshM, ThreshP: threshP}
	var vFwds, mFwds, vmpFwds, noSigFwds []float64

	// 索引
	idx := map[string]int{}
	for i, d := range dates {
		idx[d.Format("2006-01-02")] = i
	}

	for d := startT; !d.After(endT); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		i, ok := idx[ds]
		if !ok || i < 200 {
			continue
		}
		r.TotalDays++

		// V: price / power-law fair value
		price := closes[i]
		age := bitcoinAgeDays(dates[i])
		fair := math.Pow(10, 5.84*math.Log10(age)-17.01)
		val := price / fair

		// M: Mayer Multiple
		sma200 := 0.0
		for j := i - 199; j <= i; j++ {
			sma200 += closes[j]
		}
		sma200 /= 200
		mayer := price / sma200

		// P: 90d drawdown
		var dd float64
		if i >= 89 {
			max90 := closes[i-89]
			for j := i - 88; j <= i; j++ {
				if closes[j] > max90 {
					max90 = closes[j]
				}
			}
			dd = (price - max90) / max90
		}

		hasV := val > 0 && val < threshV
		hasM := mayer > 0 && mayer < threshM
		hasP := dd < threshP

		// Forward 180d
		fwdDate := d.AddDate(0, 0, 180)
		fwdIdx, ok := idx[fwdDate.Format("2006-01-02")]
		if !ok {
			continue
		}
		fwd := (closes[fwdIdx]/price - 1) * 100

		switch {
		case hasV && !hasM && !hasP:
			vFwds = append(vFwds, fwd)
		case !hasV && hasM && !hasP:
			mFwds = append(mFwds, fwd)
		case hasV && hasM && hasP:
			vmpFwds = append(vmpFwds, fwd)
		default:
			noSigFwds = append(noSigFwds, fwd)
		}
	}

	r.VN, r.VAvgFwd, r.VPosRate = stats(vFwds)
	r.MN, r.MAvgFwd, r.MPosRate = stats(mFwds)
	r.VMPN, r.VMPAvgFwd, r.VMPPosRate = stats(vmpFwds)
	r.NoSignalN, r.NoSignalAvg, _ = stats(noSigFwds)
	return r
}

func stats(fwds []float64) (int, float64, float64) {
	n := len(fwds)
	if n == 0 {
		return 0, 0, 0
	}
	sum := 0.0
	pos := 0
	for _, f := range fwds {
		sum += f
		if f > 0 {
			pos++
		}
	}
	return n, sum / float64(n), float64(pos) / float64(n)
}

func printResults(results []d3Result) {
	// Sort by V-- fwd180 descending
	// Actually, group by V threshold for readability
	fmt.Println("# 三维打分（V×M×P）阈值网格搜索结果")
	fmt.Println()
	fmt.Println("## V--（仅估值便宜）性能 — 按 V 阈值分组")
	fmt.Println()
	for _, vt := range []float64{0.5, 0.6, 0.7, 0.8, 0.9, 1.0} {
		fmt.Printf("### V < %.1f\n\n", vt)
		fmt.Println("| M阈 | P阈 | N | V-- fwd180 | 胜率 | -M- N | -M- fwd180 | -M- 胜率 | VMP N | VMP fwd180 |")
		fmt.Println("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
		for _, r := range results {
			if r.ThreshV != vt {
				continue
			}
			fmt.Printf("| %.1f | %.0f%% | %d | %+.1f%% | %.0f%% | %d | %+.1f%% | %.0f%% | %d | %+.1f%% |\n",
				r.ThreshM, r.ThreshP*-100,
				r.VN, r.VAvgFwd, r.VPosRate*100,
				r.MN, r.MAvgFwd, r.MPosRate*100,
				r.VMPN, r.VMPAvgFwd)
		}
		fmt.Println()
	}
}

// --- data fetching ---

func fetchBTCDailyClose(from, to time.Time) []pricePoint {
	const limit = 1000
	var out []pricePoint
	cursor := to.UnixMilli()
	hc := &http.Client{Timeout: 30 * time.Second}
	startMs := from.UnixMilli()
	for cursor > startMs {
		url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=BTCUSDT&interval=1d&limit=%d&endTime=%d", limit, cursor)
		req, _ := http.NewRequest("GET", url, nil)
		resp, err := hc.Do(req)
		if err != nil {
			log.Fatalf("fetch: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var raw [][]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			log.Fatalf("parse: %v", err)
		}
		if len(raw) == 0 {
			break
		}
		batch := make([]pricePoint, 0, len(raw))
		for _, row := range raw {
			ts, _ := row[0].(float64)
			closeStr, _ := row[4].(string)
			c, _ := strconv.ParseFloat(closeStr, 64)
			batch = append(batch, pricePoint{date: time.UnixMilli(int64(ts)).UTC().Truncate(24 * time.Hour), close: c})
		}
		out = append(batch, out...)
		earliest := batch[0].date.UnixMilli()
		if earliest <= startMs {
			break
		}
		cursor = earliest - 1
		if len(batch) < limit {
			break
		}
	}
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
	return filtered
}

func bitcoinAgeDays(date time.Time) float64 {
	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	return date.Sub(genesis).Hours() / 24.0
}

var _ = os.Args
var _ = strings.Join
