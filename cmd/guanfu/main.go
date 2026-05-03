// guanfu / 观复: BTC 投资盘面 CLI
//
// 「致虚极，守静笃。万物并作，吾以观复。」——《道德经》第十六章
//
// 不输出评分 / action / state。纯指标盘面，由 Claude/skill 文档完成解读。
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

	"github.com/Ricaardo/guanfu/internal/client"
	"github.com/Ricaardo/guanfu/internal/engine"
	"github.com/Ricaardo/guanfu/internal/history"
	"github.com/Ricaardo/guanfu/internal/model"
	"github.com/Ricaardo/guanfu/internal/version"
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
	jsonOut := flag.Bool("json", false, "JSON 输出")
	pretty := flag.Bool("pretty", false, "pretty JSON 输出")
	verdict := flag.Bool("verdict", false, "输出综合判断（牛熊/顶底/读盘标签）")
	verdictOnly := flag.Bool("verdict-only", false, "仅输出 verdict（隐藏指标盘）")
	domainFilter := flag.String("domain", "", "仅看单个 domain: cycle/valuation/network/positioning/macro/flow/technical/cross_asset")
	timeout := flag.Duration("timeout", 90*time.Second, "拉数据超时")
	halfLife := flag.Int("halflife", 0, "AHR 拟合半衰期（天，默认 1460）")
	historyDB := flag.String("history-db", "", "history.db 路径（默认 ~/.guanfu/history.db；GUANFU_NO_HISTORY=1 禁用）")
	plain := flag.Bool("plain", false, "纯文本输出（无 emoji / box drawing）")
	noEmoji := flag.Bool("no-emoji", false, "等同 --plain")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	if *showVersion {
		version.Print(os.Stdout, "guanfu")
		return
	}

	cfg := &model.Config{
		Weights: model.Weights{Trend: 0.30, Reversal: 0.25, Valuation: 0.25, Structure: 0.20},
		Thresholds: model.Thresholds{
			BTCMAFast:       120,
			BTCMASlow:       200,
			TopCoinCount:    50,
			AHRHalfLifeDays: *halfLife,
		},
		API: model.APIConfig{Timeout: "10s", Retries: 3, Mock: false},
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	provider := client.NewRealClient()
	snap, err := provider.GetSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch snapshot failed: %v\n", err)
		os.Exit(1)
	}

	calc := engine.NewCalculator(cfg)
	if os.Getenv("GUANFU_NO_HISTORY") != "1" {
		store, err := history.Open(*historyDB)
		if err != nil {
			log.Printf("history.Open failed (continuing without history quantiles): %v", err)
		} else {
			defer store.Close()
			calc = calc.WithHistory(store)
		}
	}
	panel := calc.BuildPanel(snap)

	var v *engine.Verdict
	if *verdict || *verdictOnly {
		v = engine.BuildVerdict(panel)
	}

	// JSON 输出
	if *jsonOut || *pretty {
		var out interface{} = panel
		if *domainFilter != "" {
			out = filterDomain(panel, *domainFilter)
		}
		if *verdictOnly {
			out = v
		} else if *verdict {
			out = struct {
				Panel   *model.IndicatorPanel `json:"panel"`
				Verdict *engine.Verdict       `json:"verdict"`
			}{Panel: panel, Verdict: v}
		}
		var b []byte
		if *pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	// 人类盘面
	if !*verdictOnly {
		printHumanPanel(panel, *domainFilter, *plain || *noEmoji)
	}
	if v != nil {
		printHumanVerdict(v, *plain || *noEmoji)
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

// filterDomain 仅保留指定 domain
func filterDomain(p *model.IndicatorPanel, name string) *model.IndicatorPanel {
	out := &model.IndicatorPanel{
		Date:          p.Date,
		Snapshot:      p.Snapshot,
		StaleWarnings: append([]string(nil), p.StaleWarnings...),
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
