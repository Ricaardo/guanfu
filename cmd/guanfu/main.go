// guanfu / 观复: BTC 投资盘面 CLI
//
// 「致虚极，守静笃。万物并作，吾以观复。」——《道德经》第十六章
//
// 默认不输出评分 / action / state；--verdict 仅输出结构化读盘，不给交易指令。
// 万物并作 = 8 域指标同时呈现；观复 = 在历史分位中看其往复回归。
//
// Usage:
//
//	guanfu                       # 人类盘面（markdown 表）
//	guanfu --json                # JSON 扁平结构（喂程序 / Claude）
//	guanfu --pretty              # pretty JSON
//	guanfu --domain cycle        # 仅看单 domain
//	guanfu --halflife 730        # AHR 拟合半衰期（默认 1460 = 4 年）
//	guanfu --timeout 180s        # 拉数据超时
//	guanfu --plain               # 纯文本输出（无 emoji / box drawing）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/pkg/client"
	"github.com/Ricaardo/guanfu/pkg/engine"
	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/guanfu/pkg/history"
	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/Ricaardo/guanfu/pkg/version"
)

// domain 中英文显示名
var domainNames = []struct {
	Key   string
	Title string
	Icon  string
}{
	{"cycle", "Cycle 周期定位", "🌊"},
	{"valuation", "Valuation 估值", "💰"},
	{"network", "Network 网络", "⛏️"},
	{"positioning", "Positioning 杠杆 & 情绪", "📊"},
	{"macro", "Macro 宏观", "🌍"},
	{"flow", "Flow 资金流", "💸"},
	{"technical", "Technical 技术指标", "📈"},
	{"cross_asset", "CrossAsset 跨资产", "🔗"},
}

func main() {
	// Subcommand detection: first positional arg before any flags.
	// For "backtest <asset>", captures the second positional arg as backtestAsset.
	subcmd := ""
	backtestAsset := "btc"
	posArgs := []string{}
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "-") {
			posArgs = append(posArgs, arg)
		}
	}
	if len(posArgs) > 0 {
		subcmd = posArgs[0]
	}
	if subcmd == "backtest" && len(posArgs) > 1 {
		backtestAsset = posArgs[1]
	}

	jsonOut := flag.Bool("json", false, "JSON 输出")
	pretty := flag.Bool("pretty", false, "pretty JSON 输出")
	verdict := flag.Bool("verdict", false, "输出综合判断（牛熊/顶底/读盘标签）")
	verdictOnly := flag.Bool("verdict-only", false, "仅输出 verdict（隐藏指标盘）")
	forecastOut := flag.Bool("forecast", false, "输出 BTC 历史相似盘面走势推演")
	forecastOnly := flag.Bool("forecast-only", false, "仅输出 forecast（隐藏指标盘）")
	forecastHorizons := flag.String("forecast-horizons", "30,90,180", "走势推演周期，逗号分隔天数，如 30,90,180")
	forecastTop := flag.Int("forecast-top", 21, "走势推演使用的历史相似样本数")
	forecastPath := flag.Bool("forecast-path", false, "输出历史相似盘面路径推演 (ASCII fan chart)")
	domainFilter := flag.String("domain", "", "仅看单个 domain: cycle/valuation/network/positioning/macro/flow/technical/cross_asset")
	timeout := flag.Duration("timeout", 90*time.Second, "拉数据超时")
	halfLife := flag.Int("halflife", 0, "AHR 拟合半衰期（天，默认 1460）")
	historyDB := flag.String("history-db", "", "history.db 路径（默认 ~/.guanfu/history.db；GUANFU_NO_HISTORY=1 禁用）")
	plain := flag.Bool("plain", false, "纯文本输出（无 emoji / box drawing）")
	noEmoji := flag.Bool("no-emoji", false, "等同 --plain")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	// Route subcommands
	switch subcmd {
	case "qqq", "spy", "gold":
		runEquityAsset(subcmd, *jsonOut, *pretty, *verdict, *verdictOnly, *forecastOut, *forecastOnly, *forecastHorizons, *forecastTop, *timeout, *plain || *noEmoji)
		return
	case "market":
		runMarketOverview(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "dca":
		runDCA(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "allocate":
		runAllocate(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "backtest":
		if backtestAsset == "all" {
			runBacktestAll(*jsonOut, *pretty, *plain || *noEmoji)
		} else {
			runBacktest(backtestAsset, *jsonOut, *pretty, *plain || *noEmoji)
		}
		return
	case "status":
		runStatus(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "hs300":
		runEquityAsset("hs300", *jsonOut, *pretty, *verdict, *verdictOnly, *forecastOut, *forecastOnly, *forecastHorizons, *forecastTop, *timeout, *plain || *noEmoji)
		return
	case "btc", "":
		// default: BTC panel (existing behavior)
	default:
		fmt.Fprintf(os.Stderr, "guanfu: unknown subcommand %q\n", subcmd)
		fmt.Fprintf(os.Stderr, "  available: btc, qqq, spy, gold, hs300, market, dca, allocate, backtest [btc|gold|qqq|spy|hs300|all], status\n")
		os.Exit(1)
	}

	if *showVersion {
		version.Print(os.Stdout, "guanfu")
		return
	}

	runBTCPanel(*jsonOut, *pretty, *verdict, *verdictOnly, *forecastOut, *forecastOnly, *forecastHorizons, *forecastTop, *timeout, *halfLife, *historyDB, *domainFilter, *plain || *noEmoji, *forecastPath)
}

// runBTCPanel is the existing BTC panel flow (unchanged from v1).
func runBTCPanel(jsonOut, pretty, verdict, verdictOnly, forecastOut, forecastOnly bool, forecastHorizonsArg string, forecastTop int, timeout time.Duration, halfLife int, historyDB, domainFilter string, plain bool, forecastPath bool) {
	cfg := &model.Config{
		Weights: model.Weights{Trend: 0.30, Reversal: 0.25, Valuation: 0.25, Structure: 0.20},
		Thresholds: model.Thresholds{
			BTCMAFast:       120,
			BTCMASlow:       200,
			TopCoinCount:    50,
			AHRHalfLifeDays: halfLife,
		},
		API: model.APIConfig{Timeout: "10s", Retries: 3, Mock: false},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	provider := client.NewRealClient()
	snap, err := provider.GetSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch snapshot failed: %v\n", err)
		os.Exit(1)
	}

	calc := engine.NewCalculator(cfg)
	if os.Getenv("GUANFU_NO_HISTORY") != "1" {
		store, err := history.Open(historyDB)
		if err != nil {
			log.Printf("history.Open failed (continuing without history quantiles): %v", err)
		} else {
			defer store.Close()
			calc = calc.WithHistory(store)
		}
	}
	panel := calc.BuildPanel(snap)

	var v *engine.Verdict
	if verdict || verdictOnly {
		v = engine.BuildVerdict(panel)
	}

	var fc *forecast.Forecast
	if forecastOut || forecastOnly {
		horizons, err := forecast.ParseHorizons(forecastHorizonsArg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse forecast horizons failed: %v\n", err)
			os.Exit(1)
		}
		points, err := forecast.PointsFromSnapshot(snap)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build forecast points failed: %v\n", err)
			os.Exit(1)
		}
		opts := forecast.DefaultOptions()
		opts.Horizons = horizons
		opts.TopK = forecastTop
		opts.Extractors = features.CoreExtractors()
		fc, err = forecast.Build(points, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build forecast failed: %v\n", err)
			os.Exit(1)
		}
	}

	// JSON output
	if jsonOut || pretty {
		var out interface{} = panel
		if domainFilter != "" && !verdict && !verdictOnly && !forecastOut && !forecastOnly {
			out = filterDomain(panel, domainFilter)
		}
		if forecastOnly {
			out = fc
		} else if verdictOnly {
			out = v
		} else if verdict || forecastOut {
			out = struct {
				Panel    *model.IndicatorPanel `json:"panel"`
				Verdict  *engine.Verdict       `json:"verdict,omitempty"`
				Forecast *forecast.Forecast    `json:"forecast,omitempty"`
			}{Panel: panel, Verdict: v, Forecast: fc}
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	// Human panel
	if !verdictOnly && !forecastOnly {
		printHumanPanel(panel, domainFilter, plain)
	}
	if v != nil && !forecastOnly {
		printHumanVerdict(v, plain)
	}
	if fc != nil {
		printHumanForecast(fc, plain)
	}
	if forecastPath && fc != nil {
		fmt.Println(forecast.BuildPathProjection(fc, 180).ASCIIFan(70))
	}
}

// runEquityAsset runs the equity flow for QQQ/SPY through the Asset interface.
func runEquityAsset(assetKey string, jsonOut, pretty, verdict, verdictOnly, forecastOut, forecastOnly bool, forecastHorizonsArg string, forecastTop int, timeout time.Duration, plain bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	a, err := engine.GetAsset(assetKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	snap, err := a.FetchSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch %s snapshot failed: %v\n", assetKey, err)
		os.Exit(1)
	}

	panel, err := a.BuildPanel(snap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build %s panel failed: %v\n", assetKey, err)
		os.Exit(1)
	}

	var v *engine.Verdict
	if verdict || verdictOnly {
		v = a.BuildVerdict(panel)
	}

	var fc *forecast.Forecast
	if forecastOut || forecastOnly {
		horizons, err := forecast.ParseHorizons(forecastHorizonsArg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse forecast horizons failed: %v\n", err)
			os.Exit(1)
		}
		opts := forecast.DefaultOptions()
		opts.Horizons = horizons
		opts.TopK = forecastTop
		fc, err = a.BuildForecast(snap, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build forecast failed: %v\n", err)
			os.Exit(1)
		}
	}

	// JSON output
	if jsonOut || pretty {
		var out interface{} = panel
		if forecastOnly {
			out = fc
		} else if verdictOnly {
			out = v
		} else if verdict || forecastOut {
			out = struct {
				Panel    *model.IndicatorPanel `json:"panel"`
				Verdict  *engine.Verdict       `json:"verdict,omitempty"`
				Forecast *forecast.Forecast    `json:"forecast,omitempty"`
			}{Panel: panel, Verdict: v, Forecast: fc}
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	// Human panel
	if !verdictOnly && !forecastOnly {
		printEquityPanel(panel, plain)
	}
	if v != nil && !forecastOnly {
		printHumanVerdict(v, plain)
	}
	if fc != nil {
		printHumanForecast(fc, plain)
	}
}

// printEquityPanel prints a human-readable equity panel.
func printEquityPanel(p *model.IndicatorPanel, plain bool) {
	if plain {
		fmt.Printf("guanfu equity panel (%s)   price: $%.2f\n", p.Date, p.Snapshot.SPYPrice+p.Snapshot.QQQPrice)
	} else {
		fmt.Printf("观复 · 权益 ETF 盘面 (%s)   价格: $%.2f\n", p.Date, p.Snapshot.SPYPrice+p.Snapshot.QQQPrice)
	}
	fmt.Println()

	equityDomains := []struct {
		Key   string
		Title string
		Icon  string
	}{
		{"technical", "Technical 技术指标", "📈"},
		{"macro", "Macro 宏观", "🌍"},
		{"positioning", "Sentiment 情绪", "📊"},
		{"valuation", "Valuation 估值", "💰"},
	}

	for _, d := range equityDomains {
		var indicators map[string]model.Indicator
		switch d.Key {
		case "technical":
			indicators = p.Technical
		case "macro":
			indicators = p.Macro
		case "positioning":
			indicators = p.Positioning
		case "valuation":
			indicators = p.Valuation
		}
		if len(indicators) == 0 {
			continue
		}
		if plain {
			fmt.Println(d.Title)
		} else {
			fmt.Printf("%s %s\n", d.Icon, d.Title)
		}
		printDomainTable(indicators)
		fmt.Println()
	}
}

func printHumanVerdict(v *engine.Verdict, plain bool) {
	bar := "═══════════════════════════════════════════════════════════"
	if plain {
		bar = "==========================================================="
	}
	fmt.Println()
	fmt.Println(bar)
	if plain {
		fmt.Printf("VERDICT  %s\n", v.Date)
	} else {
		fmt.Printf("⚖  读盘结论  %s\n", v.Date)
	}
	fmt.Println(bar)
	fmt.Printf("  Stance       : %s\n", v.Stance)
	fmt.Printf("  Regime       : %s\n", v.Regime)
	fmt.Printf("  净方向       : %+d / 8\n", v.NetDirection)
	fmt.Printf("  覆盖率       : %.0f%%   置信度：%s\n", v.Coverage*100, v.Confidence)
	fmt.Printf("  顶部接近度   : %.0f%%\n", v.TopProximity*100)
	fmt.Printf("  底部接近度   : %.0f%%\n", v.BottomProximity*100)
	fmt.Println()
	fmt.Println("  域级一致性：")
	for _, d := range v.Domains {
		fmt.Printf("    %-12s %+d  bull=%d bear=%d skip=%d  cov=%.0f%%\n",
			d.Domain, d.Vote, len(d.Bullish), len(d.Bearish), len(d.Skipped), d.Coverage*100)
	}
	if len(v.ClusterNotes) > 0 {
		fmt.Println()
		fmt.Println("  簇级去重：")
		for _, n := range v.ClusterNotes {
			fmt.Printf("    · %s\n", n)
		}
	}
	if len(v.Reasons) > 0 {
		fmt.Println()
		fmt.Println("  支持当前结论 TOP 3：")
		for _, r := range v.Reasons {
			fmt.Printf("    + %s\n", r)
		}
	}
	if len(v.CounterEvidence) > 0 {
		fmt.Println()
		fmt.Println("  反证 TOP 2：")
		for _, c := range v.CounterEvidence {
			fmt.Printf("    - %s\n", c)
		}
	}
	if len(v.KillCriteria) > 0 {
		fmt.Println()
		fmt.Println("  失效条件：")
		for _, k := range v.KillCriteria {
			fmt.Printf("    ⚠ %s\n", k)
		}
	}
	if v.MissingNote != "" {
		fmt.Println()
		fmt.Printf("  数据缺失提示：%s\n", v.MissingNote)
	}
	fmt.Println(bar)
	fmt.Println()
}

func printHumanForecast(fc *forecast.Forecast, plain bool) {
	bar := "═══════════════════════════════════════════════════════════"
	title := "走势推演"
	if plain {
		bar = "==========================================================="
		title = "FORECAST"
	}
	fmt.Println()
	fmt.Println(bar)
	if plain {
		fmt.Printf("%s  %s\n", title, fc.Date)
	} else {
		fmt.Printf("🔮  %s  %s\n", title, fc.Date)
	}
	fmt.Println(bar)
	fmt.Printf("  Method       : %s\n", fc.Method)
	fmt.Printf("  Price        : $%.2f\n", fc.CurrentPrice)
	fmt.Printf("  Coverage     : %.0f%% (%d/%d features), analogs=%d/%d, similarity=%.1f%%, confidence=%s\n",
		fc.Coverage.FeatureCoverage*100,
		fc.Coverage.FeatureCount,
		fc.Coverage.ExpectedFeatures,
		fc.Coverage.SelectedAnalogs,
		fc.Coverage.CandidateCount,
		fc.Coverage.AverageSimilarity,
		fc.Coverage.Confidence)
	fmt.Println()
	fmt.Println("  Horizon scenarios:")
	for _, h := range fc.Horizons {
		fmt.Printf("    %3dd  %-12s  up=%.0f%% range=%.0f%% down=%.0f%%  median=%+.2f%%  p10/p90=%+.2f%%/%+.2f%%  median_price=$%.0f\n",
			h.Days,
			h.DominantLabel,
			h.ProbabilityUpsideContinuation*100,
			h.ProbabilityRange*100,
			h.ProbabilityDownsidePressure*100,
			h.MedianReturnPct,
			h.P10ReturnPct,
			h.P90ReturnPct,
			h.MedianPrice)
	}
	if len(fc.Analogs) > 0 {
		fmt.Println()
		fmt.Println("  Closest analogues:")
		limit := len(fc.Analogs)
		if limit > 8 {
			limit = 8
		}
		for i := 0; i < limit; i++ {
			a := fc.Analogs[i]
			fmt.Printf("    %2d. %s  similarity=%.1f%%  price=$%.0f\n", i+1, a.Date, a.Similarity, a.Price)
		}
	}
	if len(fc.Caveats) > 0 {
		fmt.Println()
		fmt.Println("  Caveats:")
		for _, c := range fc.Caveats {
			fmt.Printf("    - %s\n", c)
		}
	}
	fmt.Println(bar)
	fmt.Println()
}

func printHumanPanel(p *model.IndicatorPanel, filter string, plain bool) {
	if plain {
		fmt.Printf("guanfu BTC panel (%s)   price: $%.2f\n", p.Date, p.Snapshot.BTCPrice)
		fmt.Printf("BTC dominance: %.2f%%   F&G: %.0f   total cap: $%.1fT\n",
			p.Snapshot.BTCDominance*100, p.Snapshot.FearGreed,
			p.Snapshot.TotalMarketCap/1e12)
	} else {
		fmt.Printf("观复 · BTC 盘面 (%s)   价格: $%.2f\n", p.Date, p.Snapshot.BTCPrice)
		fmt.Printf("├─ BTC dominance: %.2f%%   F&G: %.0f   总市值: $%.1fT\n",
			p.Snapshot.BTCDominance*100, p.Snapshot.FearGreed,
			p.Snapshot.TotalMarketCap/1e12)
	}
	fmt.Println()

	for _, d := range domainNames {
		if filter != "" && filter != d.Key {
			continue
		}
		var indicators map[string]model.Indicator
		switch d.Key {
		case "cycle":
			indicators = p.Cycle
		case "valuation":
			indicators = p.Valuation
		case "network":
			indicators = p.Network
		case "positioning":
			indicators = p.Positioning
		case "macro":
			indicators = p.Macro
		case "flow":
			indicators = p.Flow
		case "technical":
			indicators = p.Technical
		case "cross_asset":
			indicators = p.CrossAsset
		}
		if len(indicators) == 0 {
			continue
		}

		if plain {
			fmt.Println(d.Title)
		} else {
			fmt.Printf("%s %s\n", d.Icon, d.Title)
		}
		printDomainTable(indicators)
		fmt.Println()
	}

	// 数据时效性 / 待接入提示
	stale := collectStale(p)
	stale = append(stale, p.StaleWarnings...)
	stale = dedupeStrings(stale)
	if len(stale) > 0 {
		if plain {
			fmt.Println("Data tips:")
		} else {
			fmt.Println("⚠ 数据提示:")
		}
		for _, s := range stale {
			fmt.Printf("  - %s\n", s)
		}
	}

	sourceIssues := collectSourceHealthIssues(p)
	if len(sourceIssues) > 0 {
		if plain {
			fmt.Println("Source health:")
		} else {
			fmt.Println("数据源状态:")
		}
		for _, h := range sourceIssues {
			line := fmt.Sprintf("%s=%s", h.Source, h.Status)
			if h.AsOf != "" {
				line += fmt.Sprintf(" as_of=%s", h.AsOf)
			}
			if h.FallbackUsed {
				line += " fallback=true"
			}
			if h.Note != "" {
				line += fmt.Sprintf(" — %s", h.Note)
			}
			fmt.Printf("  - %s\n", line)
		}
	}
}

// printDomainTable 输出单个 domain 的指标表
func printDomainTable(indicators map[string]model.Indicator) {
	// 按 key 字典序输出（稳定显示）
	keys := make([]string, 0, len(indicators))
	for k := range indicators {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		ind := indicators[k]
		// 跳过完全 placeholder（无 value 也无 label）— 已在 stale 提示
		if ind.Value == 0 && ind.Label == "" {
			continue
		}
		// label-only 指标（如 cycle phase）单独显示
		if ind.Value == 0 && ind.Label != "" && ind.Quantile == 0 {
			fmt.Printf("  %-26s %s\n", k, ind.Label)
			continue
		}
		valStr := formatValue(k, ind.Value)
		qStr := ""
		if ind.Quantile > 0 {
			qStr = fmt.Sprintf("  q%02.0f", ind.Quantile*100)
		}
		labelStr := ""
		if ind.Label != "" {
			labelStr = fmt.Sprintf("  %s", ind.Label)
		}
		fmt.Printf("  %-26s %s%s%s\n", k, valStr, qStr, labelStr)
	}
}

// formatValue 按指标类型格式化数值
func formatValue(key string, v float64) string {
	switch {
	case key == "oil_proxy_usd" || key == "wti_crude_usd":
		return fmt.Sprintf("$%7.2f", v)
	case strings.Contains(key, "_pct") || strings.Contains(key, "_yoy"):
		return fmt.Sprintf("%+7.2f%%", v)
	case strings.Contains(key, "days_"):
		return fmt.Sprintf("%7.0f", v)
	case strings.Contains(key, "_usd"):
		return fmt.Sprintf("$%.2fM", v/1e6)
	case strings.Contains(key, "ratio") || strings.Contains(key, "multiple") || strings.Contains(key, "ahr") || strings.Contains(key, "nupl") || strings.Contains(key, "skew"):
		return fmt.Sprintf("%7.4f", v)
	case strings.Contains(key, "sma") && strings.Contains(key, "_dev"):
		return fmt.Sprintf("%+7.2f%%", v*100)
	case strings.Contains(key, "sma"):
		return fmt.Sprintf("$%.0f", v)
	default:
		// large-magnitude check
		if v >= 100 {
			return fmt.Sprintf("%7.2f", v)
		}
		return fmt.Sprintf("%7.4f", v)
	}
}

// collectStale 汇总所有 placeholder 指标的 note
func collectStale(p *model.IndicatorPanel) []string {
	var out []string
	scan := func(domain string, m map[string]model.Indicator) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ind := m[k]
			if ind.Value == 0 && ind.Label == "" && strings.Contains(ind.Source, "待接入") {
				out = append(out, fmt.Sprintf("[%s] %s — %s", domain, k, ind.Note))
			}
		}
	}
	scan("cycle", p.Cycle)
	scan("valuation", p.Valuation)
	scan("network", p.Network)
	scan("positioning", p.Positioning)
	scan("macro", p.Macro)
	scan("flow", p.Flow)
	scan("technical", p.Technical)
	scan("cross_asset", p.CrossAsset)
	return out
}

func collectSourceHealthIssues(p *model.IndicatorPanel) []model.SourceHealth {
	out := make([]model.SourceHealth, 0, len(p.SourceHealth))
	for _, h := range p.SourceHealth {
		if h.Status != "" && h.Status != "ok" {
			out = append(out, h)
		}
	}
	return out
}

// filterDomain 仅保留指定 domain
func filterDomain(p *model.IndicatorPanel, name string) *model.IndicatorPanel {
	out := &model.IndicatorPanel{
		Date:          p.Date,
		Snapshot:      p.Snapshot,
		StaleWarnings: append([]string(nil), p.StaleWarnings...),
		SourceHealth:  append([]model.SourceHealth(nil), p.SourceHealth...),
	}
	switch name {
	case "cycle":
		out.Cycle = p.Cycle
	case "valuation":
		out.Valuation = p.Valuation
	case "network":
		out.Network = p.Network
	case "positioning":
		out.Positioning = p.Positioning
	case "macro":
		out.Macro = p.Macro
	case "flow":
		out.Flow = p.Flow
	case "technical":
		out.Technical = p.Technical
	case "cross_asset":
		out.CrossAsset = p.CrossAsset
	}
	return out
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
