// Backtest CLI — kNN forecast validation comparing v1 vs v2 feature sets.

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/forecast/backtest"
	"github.com/Ricaardo/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/guanfu/pkg/store"
)

func runBacktest(asset string, jsonOut, pretty, plain bool) {
	var ok bool
	asset, ok = normalizeBacktestAsset(asset)
	if !ok {
		fmt.Fprintf(os.Stderr, "backtest %s: unsupported asset (available: btc, qqq, spy, gold, all)\n", asset)
		os.Exit(1)
	}
	s := &store.PriceStore{}
	pricePoints, err := s.Load(asset)
	if err != nil || len(pricePoints) < 500 {
		fmt.Fprintf(os.Stderr, "backtest %s: need 500+ days of price data in PriceStore\n", asset)
		os.Exit(1)
	}

	points := make([]forecast.Point, len(pricePoints))
	for i, p := range pricePoints {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: "price_store:" + asset}
	}

	hasCrossAsset := asset == "btc"
	if hasCrossAsset {
		found := false
		for _, a := range []string{"gold", "qqq", "spy", "uup", "tlt"} {
			if count, _ := s.Count(a); count >= 500 {
				found = true
				break
			}
		}
		hasCrossAsset = found
	}

	horizons := []int{30, 90, 180}
	optsV1 := forecast.Options{
		Horizons: horizons, TopK: 21, StepDays: 30,
		Extractors: features.CoreExtractors(), MinFeatures: 6,
	}

	if jsonOut || pretty {
		runBacktestJSON(asset, points, hasCrossAsset, horizons, optsV1, pretty)
		return
	}

	runBacktestHuman(asset, points, hasCrossAsset, horizons, optsV1, plain)
}

func runBacktestJSON(asset string, points []forecast.Point, hasCrossAsset bool, horizons []int, optsV1 forecast.Options, pretty bool) {
	type btH struct {
		Days        int     `json:"days"`
		SampleCount int     `json:"sample_count"`
		DirHitRate  float64 `json:"dir_hit_rate_pct"`
		PITMean     float64 `json:"pit_mean"`
		CRPS        float64 `json:"crps"`
	}
	type btR struct {
		Version    string `json:"version"`
		Features   string `json:"features"`
		TotalTests int    `json:"total_tests"`
		Horizons   []btH  `json:"horizons"`
	}

	results := []btR{}
	r1, _ := backtest.Run(points, 800, 60, optsV1.Extractors, horizons)
	if r1 != nil {
		br := btR{Version: "v1_core", Features: "11 core price", TotalTests: r1.TotalTests}
		for _, h := range horizons {
			if hm := r1.ByHorizon[h]; hm != nil {
				br.Horizons = append(br.Horizons, btH{
					Days: h, SampleCount: hm.SampleCount,
					DirHitRate: math.Round(hm.DirectionHitRate()*10000) / 100,
					PITMean:    math.Round(hm.PITMean()*1000) / 1000,
					CRPS:       math.Round(hm.CRPSScore()*10000) / 10000,
				})
			}
		}
		results = append(results, br)
	}

	if hasCrossAsset {
		ca := features.NewCrossAssetData()
		ca.LoadFromPriceStore()
		allEx := append(features.CoreExtractors(), ca.Extractors()...)
		r2, _ := backtest.Run(points, 800, 60, allEx, horizons)
		if r2 != nil {
			br := btR{Version: "v2_cross_asset", Features: "11 core + 6 cross", TotalTests: r2.TotalTests}
			for _, h := range horizons {
				if hm := r2.ByHorizon[h]; hm != nil {
					br.Horizons = append(br.Horizons, btH{
						Days: h, SampleCount: hm.SampleCount,
						DirHitRate: math.Round(hm.DirectionHitRate()*10000) / 100,
						PITMean:    math.Round(hm.PITMean()*1000) / 1000,
						CRPS:       math.Round(hm.CRPSScore()*10000) / 10000,
					})
				}
			}
			results = append(results, br)
		}
	}

	out := map[string]interface{}{
		"asset":                 asset,
		"backtest":              results,
		"price_days":            len(points),
		"cross_asset_available": hasCrossAsset,
	}
	var b []byte
	if pretty {
		b, _ = json.MarshalIndent(out, "", "  ")
	} else {
		b, _ = json.Marshal(out)
	}
	fmt.Println(string(b))
}

func runBacktestHuman(asset string, points []forecast.Point, hasCrossAsset bool, horizons []int, optsV1 forecast.Options, plain bool) {
	title := fmt.Sprintf("观复 · kNN 预测回测 (%s)", asset)
	if plain {
		title = fmt.Sprintf("kNN Forecast Backtest (%s)", asset)
	}
	fmt.Printf("%s\n", title)
	fmt.Printf("  历史: %d 天  跨资产数据: %v\n", len(points), hasCrossAsset)
	fmt.Println(strings.Repeat("─", 72))

	fmt.Println("V1 — 纯价格特征 (11 core)")
	r1, err := backtest.Run(points, 800, 60, optsV1.Extractors, horizons)
	if err != nil {
		fmt.Printf("  失败: %v\n", err)
	} else {
		fmt.Printf("  测试窗口: %d\n", r1.TotalTests)
		fmt.Printf("  %-6s %8s %10s %8s %8s\n", "周期", "样本数", "方向命中", "PIT", "CRPS")
		for _, h := range horizons {
			if hm := r1.ByHorizon[h]; hm != nil {
				fmt.Printf("  %3dd   %7d %9.1f%% %7.2f %7.4f\n",
					h, hm.SampleCount, hm.DirectionHitRate()*100, hm.PITMean(), hm.CRPSScore())
			}
		}
	}

	if hasCrossAsset {
		fmt.Println()
		fmt.Println("V2 — 核心 + 跨资产特征 (17 features)")
		ca := features.NewCrossAssetData()
		ca.LoadFromPriceStore()
		allEx := append(features.CoreExtractors(), ca.Extractors()...)
		r2, err := backtest.Run(points, 800, 60, allEx, horizons)
		if err != nil {
			fmt.Printf("  失败: %v\n", err)
		} else {
			fmt.Printf("  测试窗口: %d\n", r2.TotalTests)
			fmt.Printf("  %-6s %8s %10s %8s %8s\n", "周期", "样本数", "方向命中", "PIT", "CRPS")
			for _, h := range horizons {
				if hm := r2.ByHorizon[h]; hm != nil {
					delta := ""
					if hm1 := r1.ByHorizon[h]; hm1 != nil && hm1.SampleCount > 0 {
						d := hm.DirectionHitRate() - hm1.DirectionHitRate()
						delta = fmt.Sprintf("  Δ%+.1f%%", d*100)
					}
					fmt.Printf("  %3dd   %7d %9.1f%% %7.2f %7.4f%s\n",
						h, hm.SampleCount, hm.DirectionHitRate()*100, hm.PITMean(), hm.CRPSScore(), delta)
				}
			}
		}
	}

	fmt.Println(strings.Repeat("─", 72))
	fmt.Println("方向命中 >50% = 优于随机。Δ = v2-v1 差异。PIT~0.5=校准好。CRPS↓=优。")
	fmt.Println("不是投资建议。")
}

func normalizeBacktestAsset(asset string) (string, bool) {
	asset = strings.ToLower(strings.TrimSpace(asset))
	if asset == "" {
		asset = "btc"
	}
	switch asset {
	case "btc", "qqq", "spy", "gold":
		return asset, true
	default:
		return asset, false
	}
}
