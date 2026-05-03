// Verdict 引擎 — 把 8 域指标盘聚合成结构化牛熊判断。
//
// 设计原则（与观复 v2 哲学一致）：
//   - 不输出"BUY $X"式硬交易指令
//   - 不输出 0-100 单一压缩分（已废）
//   - 输出多维结构化判断：净方向、覆盖率、牛熊状态、顶/底接近度、读盘标签
//
// 关键不变量：
//   - 任何 Missing=true 的指标必须自动跳过，不参与计票、不影响覆盖率分母
//     之外的统计；只在 coverage 字段中如实反映"少了多少指标"
//   - 簇级去重：估值簇 (mayer+sma_200w_dev+ahr999)、情绪簇 (F&G+DVOL)
//     在同向时只算一票
//   - 失效门槛：覆盖率 < 50% 时，confidence 自动降为"低"，stance 加 "(低覆盖)"
//
// 输出消费者：CLI `--verdict` 子命令、guanfu-mcp、回测器、SKILL.md 读盘协议。

package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Ricaardo/guanfu/internal/model"
)

// Verdict 综合读盘结论。
type Verdict struct {
	Date            string       `json:"date"`
	NetDirection    int          `json:"net_direction"`     // [-8, +8] 域级一致性
	Coverage        float64      `json:"coverage"`          // 0-1，可用指标占比
	Confidence      string       `json:"confidence"`        // "高" / "中" / "低"
	Regime          string       `json:"regime"`            // "牛市" / "熊市" / "过渡"
	Stance          string       `json:"stance"`            // 读盘口径
	TopProximity    float64      `json:"top_proximity"`     // 0-1
	BottomProximity float64      `json:"bottom_proximity"`  // 0-1
	Domains         []DomainVote `json:"domains"`
	Reasons         []string     `json:"reasons"`           // 支持当前结论 top 3 指标
	CounterEvidence []string     `json:"counter_evidence"`  // 反证 top 2
	KillCriteria    []string     `json:"kill_criteria"`     // 失效条件
	ClusterNotes    []string     `json:"cluster_notes"`     // 簇级去重说明
	MissingNote     string       `json:"missing_note,omitempty"`
}

// DomainVote 单个域的方向与依据。
type DomainVote struct {
	Domain   string   `json:"domain"`
	Vote     int      `json:"vote"`     // -1 / 0 / +1
	Bullish  []string `json:"bullish,omitempty"`
	Bearish  []string `json:"bearish,omitempty"`
	Skipped  []string `json:"skipped,omitempty"` // Missing=true 的指标
	Coverage float64  `json:"coverage"`          // 该域已实现指标的可用率
}

// BuildVerdict 主入口。从已构造好的 IndicatorPanel 读，幂等，无副作用。
func BuildVerdict(p *model.IndicatorPanel) *Verdict {
	v := &Verdict{
		Date: p.Date,
	}

	cycle := voteCycle(p)
	val := voteValuation(p)
	net := voteNetwork(p)
	pos := votePositioning(p)
	macro := voteMacro(p)
	flow := voteFlow(p)
	tech := voteTechnical(p)
	cross := voteCrossAsset(p)

	// 簇级去重：估值簇 (cycle 的 mayer/sma_200w_dev + valuation 的 ahr999)
	// 当两个域因同源估值信号同向时，扣减一个域为 0，避免重复计数。
	cycle, val, clusterNote := dedupValuationCluster(cycle, val)
	if clusterNote != "" {
		v.ClusterNotes = append(v.ClusterNotes, clusterNote)
	}

	v.Domains = []DomainVote{cycle, val, net, pos, macro, flow, tech, cross}

	// 净方向 + 覆盖率
	netDir := 0
	availableTotal := 0
	expectedTotal := 0
	for _, d := range v.Domains {
		netDir += d.Vote
		availSlice := len(d.Bullish) + len(d.Bearish)
		availableTotal += availSlice
		expectedTotal += availSlice + len(d.Skipped)
	}
	v.NetDirection = netDir
	if expectedTotal > 0 {
		v.Coverage = float64(availableTotal) / float64(expectedTotal)
	} else {
		v.Coverage = 0
	}

	v.Confidence = mapConfidence(v.Coverage, abs(netDir))
	v.Stance = mapStance(netDir)
	v.Regime = mapRegime(netDir, v.Coverage)

	// Top/Bottom proximity (0-1，独立通道；与 stance 互补不冗余)
	v.TopProximity = computeTopProximity(p)
	v.BottomProximity = computeBottomProximity(p)

	v.Reasons, v.CounterEvidence = pickEvidence(p, netDir)
	v.KillCriteria = killCriteria(netDir, v.TopProximity, v.BottomProximity)

	skipped := collectSkipped(v.Domains)
	if len(skipped) > 0 {
		v.MissingNote = fmt.Sprintf("跳过 %d 个缺数据指标：%s。覆盖率 %.0f%%。",
			len(skipped), strings.Join(skipped, ", "), v.Coverage*100)
	}

	return v
}

// ---------- 域级投票 ----------

// 投票约定：
//   bull condition 命中 → +1 票，附 indicator 名进 Bullish
//   bear condition 命中 → -1 票，附 indicator 名进 Bearish
//   两边都命中 → 0 (相互抵消)
//   两边都没命中 → 0 (中性)
//   指标 Missing → 加入 Skipped，不计票

func voteCycle(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "cycle"}
	bull, bear := 0, 0
	check := func(key string, bullCond, bearCond func(model.Indicator) bool) {
		ind, ok := p.Cycle[key]
		if !ok {
			return
		}
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, key)
			return
		}
		if bullCond != nil && bullCond(ind) {
			d.Bullish = append(d.Bullish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bull++
		}
		if bearCond != nil && bearCond(ind) {
			d.Bearish = append(d.Bearish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bear++
		}
	}
	check("mayer_multiple",
		func(i model.Indicator) bool { return i.Value < 1.0 && i.Value > 0 },
		func(i model.Indicator) bool { return i.Value > 2.4 })
	check("sma_200w_dev",
		func(i model.Indicator) bool { return i.Value < 0 },
		func(i model.Indicator) bool { return i.Value > 150 })
	check("pi_cycle_top_ratio",
		nil,
		func(i model.Indicator) bool { return i.Value >= 0.85 })
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func voteValuation(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "valuation"}
	bull, bear := 0, 0
	check := func(key string, bullCond, bearCond func(float64) bool) {
		ind, ok := p.Valuation[key]
		if !ok {
			return
		}
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, key)
			return
		}
		if bullCond != nil && bullCond(ind.Value) {
			d.Bullish = append(d.Bullish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bull++
		}
		if bearCond != nil && bearCond(ind.Value) {
			d.Bearish = append(d.Bearish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bear++
		}
	}
	check("ahr999",
		func(v float64) bool { return v < 0.8 && v > 0 },
		func(v float64) bool { return v > 2.0 })
	check("mvrv_z_score",
		func(v float64) bool { return v < 0 },
		func(v float64) bool { return v > 5 })
	check("nupl",
		func(v float64) bool { return v < 0 },
		func(v float64) bool { return v > 0.75 })
	check("price_to_realized_dev_pct",
		func(v float64) bool { return v < 0 },
		func(v float64) bool { return v > 200 })
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func voteNetwork(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "network"}
	bull, bear := 0, 0
	if ind, ok := p.Network["hash_ribbons"]; ok {
		if !ind.IsAvailable() && ind.Label == "" {
			d.Skipped = append(d.Skipped, "hash_ribbons")
		} else {
			lbl := ind.Label
			if strings.Contains(lbl, "上行") {
				d.Bullish = append(d.Bullish, "hash_ribbons=上行")
				bull++
			} else if strings.Contains(lbl, "下行") {
				d.Bearish = append(d.Bearish, "hash_ribbons=下行")
				bear++
			}
		}
	}
	if ind, ok := p.Network["difficulty_change_pct"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "difficulty_change_pct")
		} else {
			if ind.Value > 5 {
				d.Bullish = append(d.Bullish, fmt.Sprintf("difficulty=+%.1f%%", ind.Value))
				bull++
			} else if ind.Value < -5 {
				d.Bearish = append(d.Bearish, fmt.Sprintf("difficulty=%.1f%%", ind.Value))
				bear++
			}
		}
	}
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func votePositioning(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "positioning"}
	bull, bear := 0, 0
	check := func(key string, bullCond, bearCond func(model.Indicator) bool) {
		ind, ok := p.Positioning[key]
		if !ok {
			return
		}
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, key)
			return
		}
		if bullCond != nil && bullCond(ind) {
			d.Bullish = append(d.Bullish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bull++
		}
		if bearCond != nil && bearCond(ind) {
			d.Bearish = append(d.Bearish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bear++
		}
	}
	check("funding_rate_pct",
		func(i model.Indicator) bool { return i.Value < 0 },
		func(i model.Indicator) bool { return i.Value > 0.05 })
	check("oi_to_mc",
		func(i model.Indicator) bool { return i.Value < 0.015 },
		func(i model.Indicator) bool { return i.Value > 0.035 })
	check("fear_greed",
		func(i model.Indicator) bool { return i.Value < 25 },
		func(i model.Indicator) bool { return i.Value > 80 })
	check("skew_25d_pct",
		func(i model.Indicator) bool { return i.Value > 5 },
		func(i model.Indicator) bool { return i.Value < -3 })
	check("dvol",
		func(i model.Indicator) bool { return i.Quantile > 0.85 },
		func(i model.Indicator) bool { return i.Quantile > 0 && i.Quantile < 0.15 })
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func voteMacro(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "macro"}
	bull, bear := 0, 0
	check := func(key string, bullCond, bearCond func(float64) bool) {
		ind, ok := p.Macro[key]
		if !ok {
			return
		}
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, key)
			return
		}
		if bullCond != nil && bullCond(ind.Value) {
			d.Bullish = append(d.Bullish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bull++
		}
		if bearCond != nil && bearCond(ind.Value) {
			d.Bearish = append(d.Bearish, fmt.Sprintf("%s=%.4g", key, ind.Value))
			bear++
		}
	}
	check("m2_yoy",
		func(v float64) bool { return v > 5 },
		func(v float64) bool { return v < 0 })
	check("real_yield_10y_pct",
		func(v float64) bool { return v < 1 },
		func(v float64) bool { return v > 2.5 })
	check("dxy_60d_trend_pct",
		func(v float64) bool { return v < -1 },
		func(v float64) bool { return v > 1 })
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func voteFlow(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "flow"}
	bull, bear := 0, 0
	if ind, ok := p.Flow["etf_net_flow_30d_usd"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "etf_net_flow_30d_usd")
		} else {
			if ind.Value > 1e9 {
				d.Bullish = append(d.Bullish, fmt.Sprintf("etf_30d=+$%.1fB", ind.Value/1e9))
				bull++
			} else if ind.Value < -1e9 {
				d.Bearish = append(d.Bearish, fmt.Sprintf("etf_30d=$%.1fB", ind.Value/1e9))
				bear++
			}
		}
	}
	if ind, ok := p.Flow["stablecoin_supply_30d_pct"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "stablecoin_supply_30d_pct")
		} else {
			if ind.Value > 5 {
				d.Bullish = append(d.Bullish, fmt.Sprintf("stables_30d=+%.1f%%", ind.Value))
				bull++
			} else if ind.Value < -3 {
				d.Bearish = append(d.Bearish, fmt.Sprintf("stables_30d=%.1f%%", ind.Value))
				bear++
			}
		}
	}
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func voteTechnical(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "technical"}
	bull, bear := 0, 0
	if ind, ok := p.Technical["rsi_14"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "rsi_14")
		} else {
			if ind.Value < 30 {
				d.Bullish = append(d.Bullish, fmt.Sprintf("rsi=%.1f", ind.Value))
				bull++
			} else if ind.Value > 75 {
				d.Bearish = append(d.Bearish, fmt.Sprintf("rsi=%.1f", ind.Value))
				bear++
			}
		}
	}
	if ind, ok := p.Technical["macd_histogram"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "macd_histogram")
		} else {
			if ind.Value > 0 {
				d.Bullish = append(d.Bullish, "macd_hist>0")
				bull++
			} else if ind.Value < 0 {
				d.Bearish = append(d.Bearish, "macd_hist<0")
				bear++
			}
		}
	}
	if ind, ok := p.Technical["ma_alignment"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "ma_alignment")
		} else {
			if ind.Value > 0 {
				d.Bullish = append(d.Bullish, "MA50>MA200")
				bull++
			} else if ind.Value < 0 {
				d.Bearish = append(d.Bearish, "MA50<MA200")
				bear++
			}
		}
	}
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

func voteCrossAsset(p *model.IndicatorPanel) DomainVote {
	d := DomainVote{Domain: "cross_asset"}
	bull, bear := 0, 0
	if ind, ok := p.CrossAsset["btc_spy_corr_30d"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "btc_spy_corr_30d")
		} else {
			if ind.Value < 0.3 {
				d.Bullish = append(d.Bullish, fmt.Sprintf("btc_spy_corr=%.2f (独立行情)", ind.Value))
				bull++
			} else if ind.Value > 0.7 {
				d.Bearish = append(d.Bearish, fmt.Sprintf("btc_spy_corr=%.2f (高 beta)", ind.Value))
				bear++
			}
		}
	}
	if ind, ok := p.CrossAsset["rel_strength_90d_gold"]; ok {
		if !ind.IsAvailable() {
			d.Skipped = append(d.Skipped, "rel_strength_90d_gold")
		} else {
			if ind.Value > 10 {
				d.Bullish = append(d.Bullish, fmt.Sprintf("BTC 跑赢 Gold %.1f pp", ind.Value))
				bull++
			} else if ind.Value < -10 {
				d.Bearish = append(d.Bearish, fmt.Sprintf("BTC 跑输 Gold %.1f pp", ind.Value))
				bear++
			}
		}
	}
	d.Vote = sign(bull - bear)
	d.Coverage = coverageOf(d)
	return d
}

// dedupValuationCluster — Cycle 域和 Valuation 域因 mayer/sma_200w_dev/ahr999
// 同源估值信号同向时，弱化一个域为 0，避免重复计票。
//
// 规则：
//   - 两个域 vote 同号（都 +1 或都 -1）
//   - 两个域的 Bullish/Bearish 列表里都有估值类指标
//   → Valuation 域保留为权威投票，Cycle 域弱化为 0；返回说明字符串。
//
// （Cycle 域里只有 mayer/sma_200w_dev/pi_cycle 三个指标；如果 cycle 同向是
// 因为 pi_cycle 而非 mayer/dev，则不应去重——我们检查 Cycle.Bullish/Bearish
// 是否含 mayer 或 sma_200w_dev。）
func dedupValuationCluster(cycle, val DomainVote) (DomainVote, DomainVote, string) {
	if cycle.Vote == 0 || val.Vote == 0 || cycle.Vote != val.Vote {
		return cycle, val, ""
	}
	cycleHasValSignal := containsAny(cycle.Bullish, "mayer", "sma_200w_dev") ||
		containsAny(cycle.Bearish, "mayer", "sma_200w_dev")
	if !cycleHasValSignal {
		return cycle, val, ""
	}
	original := cycle.Vote
	note := fmt.Sprintf("估值簇去重：cycle 与 valuation 因同源估值同 %+d，cycle 弱化为 0（pi_cycle 仍独立计入；不重复加权）", original)
	cycle.Vote = 0
	return cycle, val, note
}

// ---------- Stance / Regime / Confidence 映射 ----------

func mapStance(net int) string {
	switch {
	case net >= 5:
		return "强积累倾向"
	case net >= 3:
		return "偏积累倾向"
	case net >= 1:
		return "持有观察倾向"
	case net == 0:
		return "等待"
	case net >= -2:
		return "防守倾向"
	case net >= -4:
		return "高防守倾向"
	default:
		return "分配 / 避险风险"
	}
}

func mapRegime(net int, coverage float64) string {
	if coverage < 0.5 {
		return "数据不足，无法定型"
	}
	switch {
	case net >= 4:
		return "牛市"
	case net <= -4:
		return "熊市"
	case net >= 2:
		return "牛市偏弱 / 过渡"
	case net <= -2:
		return "熊市偏弱 / 过渡"
	default:
		return "过渡 / 震荡"
	}
}

func mapConfidence(coverage float64, absNet int) string {
	if coverage < 0.5 {
		return "低（覆盖率不足）"
	}
	if coverage < 0.7 {
		if absNet >= 4 {
			return "中"
		}
		return "低"
	}
	if absNet >= 5 {
		return "高"
	}
	if absNet >= 3 {
		return "中"
	}
	return "低"
}

// ---------- 顶/底接近度（独立通道，0-1） ----------

// computeTopProximity — 综合 8+ 个顶部信号的 weighted score。
// 每个信号触发加固定权重，最后归一化到 0-1。
func computeTopProximity(p *model.IndicatorPanel) float64 {
	score := 0.0
	max := 0.0
	add := func(weight float64, triggered bool) {
		max += weight
		if triggered {
			score += weight
		}
	}
	// Pi Cycle — 历史 100% 命中顶部信号，最高权重
	if ind, ok := p.Cycle["pi_cycle_top_ratio"]; ok && ind.IsAvailable() {
		add(3.0, ind.Value >= 1.0)
	}
	if ind, ok := p.Cycle["mayer_multiple"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value > 2.4)
	}
	if ind, ok := p.Cycle["sma_200w_dev"]; ok && ind.IsAvailable() {
		add(1.5, ind.Value > 150)
	}
	if ind, ok := p.Valuation["mvrv_z_score"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value > 7)
	}
	if ind, ok := p.Valuation["nupl"]; ok && ind.IsAvailable() {
		add(1.5, ind.Value > 0.75)
	}
	if ind, ok := p.Positioning["funding_rate_pct"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value > 0.05)
	}
	if ind, ok := p.Positioning["oi_to_mc"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value > 0.04)
	}
	if ind, ok := p.Positioning["fear_greed"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value > 80)
	}
	if ind, ok := p.Positioning["skew_25d_pct"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value < -3)
	}
	if ind, ok := p.Network["mempool_mb"]; ok && ind.IsAvailable() {
		add(0.5, ind.Value > 100)
	}
	if ind, ok := p.Technical["rsi_14"]; ok && ind.IsAvailable() {
		add(0.5, ind.Value > 80)
	}
	if max == 0 {
		return 0
	}
	return clamp01(score / max)
}

func computeBottomProximity(p *model.IndicatorPanel) float64 {
	score := 0.0
	max := 0.0
	add := func(weight float64, triggered bool) {
		max += weight
		if triggered {
			score += weight
		}
	}
	if ind, ok := p.Cycle["mayer_multiple"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value < 0.7 && ind.Value > 0)
	}
	if ind, ok := p.Cycle["sma_200w_dev"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value < 0)
	}
	if ind, ok := p.Valuation["ahr999"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value < 0.45 && ind.Value > 0)
	}
	if ind, ok := p.Valuation["mvrv_z_score"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value < 0)
	}
	if ind, ok := p.Valuation["price_to_realized_dev_pct"]; ok && ind.IsAvailable() {
		add(2.0, ind.Value < 0)
	}
	if ind, ok := p.Network["hash_ribbons"]; ok && strings.Contains(ind.Label, "下行") {
		add(1.5, true)
	} else {
		max += 1.5 // counts toward denominator
	}
	if ind, ok := p.Network["difficulty_change_pct"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value < -7)
	}
	if ind, ok := p.Positioning["funding_rate_pct"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value < 0)
	}
	if ind, ok := p.Positioning["fear_greed"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value < 20)
	}
	if ind, ok := p.Positioning["skew_25d_pct"]; ok && ind.IsAvailable() {
		add(1.0, ind.Value > 5)
	}
	if ind, ok := p.Technical["rsi_14"]; ok && ind.IsAvailable() {
		add(0.5, ind.Value < 28)
	}
	if max == 0 {
		return 0
	}
	return clamp01(score / max)
}

// ---------- Evidence picker / Kill criteria ----------

func pickEvidence(p *model.IndicatorPanel, netDir int) (top []string, against []string) {
	// 收集所有 available 指标的"方向票"+ 强度。强度 = |q - 0.5| × 2 时（q ∈ [0,1]），
	// 范围回到 [0,1]。q 缺失时用 fallback weight 0.5。
	type item struct {
		domain string
		key    string
		dir    int
		mag    float64
		val    float64
	}
	var items []item
	walk := func(domain string, m map[string]model.Indicator) {
		for k, ind := range m {
			if !ind.IsAvailable() {
				continue
			}
			d := indicatorDirection(domain, k, ind)
			if d == 0 {
				continue
			}
			mag := 0.5
			if ind.Quantile > 0 {
				mag = math.Abs(ind.Quantile-0.5) * 2
			}
			items = append(items, item{domain, k, d, mag, ind.Value})
		}
	}
	walk("cycle", p.Cycle)
	walk("valuation", p.Valuation)
	walk("network", p.Network)
	walk("positioning", p.Positioning)
	walk("macro", p.Macro)
	walk("flow", p.Flow)
	walk("technical", p.Technical)
	walk("cross_asset", p.CrossAsset)

	if len(items) == 0 {
		return nil, nil
	}
	consensusDir := sign(netDir)
	if consensusDir == 0 {
		consensusDir = +1 // 中性时默认按看涨证据 / 看跌证据各列出最强
	}
	supporting := make([]item, 0, len(items))
	contradicting := make([]item, 0, len(items))
	for _, it := range items {
		if it.dir == consensusDir {
			supporting = append(supporting, it)
		} else if it.dir == -consensusDir {
			contradicting = append(contradicting, it)
		}
	}
	sort.Slice(supporting, func(i, j int) bool { return supporting[i].mag > supporting[j].mag })
	sort.Slice(contradicting, func(i, j int) bool { return contradicting[i].mag > contradicting[j].mag })

	for i := 0; i < len(supporting) && i < 3; i++ {
		s := supporting[i]
		top = append(top, fmt.Sprintf("%s/%s = %.4g (强度 %.0f%%)", s.domain, s.key, s.val, s.mag*100))
	}
	for i := 0; i < len(contradicting) && i < 2; i++ {
		s := contradicting[i]
		against = append(against, fmt.Sprintf("%s/%s = %.4g (强度 %.0f%%)", s.domain, s.key, s.val, s.mag*100))
	}
	return top, against
}

// indicatorDirection — 单个指标的 -1/0/+1 方向倾向。
// 与域投票阈值复用：尽量保持逻辑一致。
func indicatorDirection(domain, key string, ind model.Indicator) int {
	v := ind.Value
	switch domain {
	case "cycle":
		switch key {
		case "mayer_multiple":
			if v < 1.0 && v > 0 {
				return +1
			}
			if v > 2.4 {
				return -1
			}
		case "sma_200w_dev":
			if v < 0 {
				return +1
			}
			if v > 150 {
				return -1
			}
		case "pi_cycle_top_ratio":
			if v >= 0.85 {
				return -1
			}
		}
	case "valuation":
		switch key {
		case "ahr999":
			if v < 0.8 && v > 0 {
				return +1
			}
			if v > 2.0 {
				return -1
			}
		case "mvrv_z_score":
			if v < 0 {
				return +1
			}
			if v > 5 {
				return -1
			}
		case "nupl":
			if v < 0 {
				return +1
			}
			if v > 0.75 {
				return -1
			}
		case "price_to_realized_dev_pct":
			if v < 0 {
				return +1
			}
			if v > 200 {
				return -1
			}
		}
	case "network":
		if key == "hash_ribbons" {
			if strings.Contains(ind.Label, "上行") {
				return +1
			}
			if strings.Contains(ind.Label, "下行") {
				return -1
			}
		}
		if key == "difficulty_change_pct" {
			if v > 5 {
				return +1
			}
			if v < -5 {
				return -1
			}
		}
	case "positioning":
		switch key {
		case "funding_rate_pct":
			if v < 0 {
				return +1
			}
			if v > 0.05 {
				return -1
			}
		case "oi_to_mc":
			if v < 0.015 {
				return +1
			}
			if v > 0.035 {
				return -1
			}
		case "fear_greed":
			if v < 25 {
				return +1
			}
			if v > 80 {
				return -1
			}
		case "skew_25d_pct":
			if v > 5 {
				return +1
			}
			if v < -3 {
				return -1
			}
		}
	case "macro":
		switch key {
		case "m2_yoy":
			if v > 5 {
				return +1
			}
			if v < 0 {
				return -1
			}
		case "real_yield_10y_pct":
			if v < 1 {
				return +1
			}
			if v > 2.5 {
				return -1
			}
		}
	case "flow":
		if key == "etf_net_flow_30d_usd" {
			if v > 1e9 {
				return +1
			}
			if v < -1e9 {
				return -1
			}
		}
	case "technical":
		switch key {
		case "rsi_14":
			if v < 30 {
				return +1
			}
			if v > 75 {
				return -1
			}
		case "macd_histogram":
			if v > 0 {
				return +1
			}
			if v < 0 {
				return -1
			}
		case "ma_alignment":
			if v > 0 {
				return +1
			}
			if v < 0 {
				return -1
			}
		}
	case "cross_asset":
		if key == "btc_spy_corr_30d" {
			if v < 0.3 {
				return +1
			}
			if v > 0.7 {
				return -1
			}
		}
	}
	return 0
}

func killCriteria(netDir int, topProx, botProx float64) []string {
	out := []string{}
	switch sign(netDir) {
	case +1:
		out = append(out,
			"Pi Cycle 触发 (≥1.0) → 顶部信号，立即重新评估",
			"hash_ribbons 翻为下行 + difficulty < -5% → 矿工投降，结构性变盘",
			"3+ 域在 14 天内从看涨翻看跌 → regime change warning")
		if topProx > 0.5 {
			out = append(out, fmt.Sprintf("顶部接近度已 %.0f%% → 任意一个新顶部信号触发都应即刻减仓", topProx*100))
		}
	case -1:
		out = append(out,
			"价格突破 mayer=1.0 + ETF 30d 转正 → 趋势可能已反转",
			"funding 持续 < 0 + RSI < 30 + STH-SOPR 突破 1（链上接入后）→ 短期底确认",
			"3+ 域在 14 天内从看跌翻看涨 → regime change warning")
		if botProx > 0.5 {
			out = append(out, fmt.Sprintf("底部接近度已 %.0f%% → 任意一个新底部信号触发都应即刻调高仓位", botProx*100))
		}
	default:
		out = append(out,
			"任意 3 个域同向翻转 → 等待结束，按方向跟随",
			"覆盖率回到 ≥70% → 重新跑 verdict 取代当前等待")
	}
	return out
}

// ---------- 工具 ----------

func sign(x int) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	default:
		return 0
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func containsAny(slice []string, needles ...string) bool {
	for _, s := range slice {
		for _, n := range needles {
			if strings.Contains(s, n) {
				return true
			}
		}
	}
	return false
}

func coverageOf(d DomainVote) float64 {
	available := len(d.Bullish) + len(d.Bearish)
	total := available + len(d.Skipped)
	if total == 0 {
		return 0
	}
	// 中性指标（既不 bull 也不 bear 但 available）也应该算入分子；
	// 但当前我们只把命中阈值的指标加入 Bullish/Bearish。
	// 这里 available/total 应理解为"非 Missing 且参与判断的占比"。
	return float64(available) / float64(total)
}

func collectSkipped(votes []DomainVote) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range votes {
		for _, k := range v.Skipped {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
