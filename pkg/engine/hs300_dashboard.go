// CSI300 (沪深300) enhanced dashboard with Chinese macro dimensions.
//
// Domains (6, matching equity dashboard structure):
//   1. Valuation: PE band position, PB, dividend yield estimate
//   2. Technical: RSI, MACD, BB, volatility (same as equity)
//   3. Flow: volume trend, northbound proxy (large-cap flow)
//   4. Macro (China): CNY/USD trend, LPR proxy, PMI proxy
//   5. Sentiment: margin balance proxy, volume climax detection
//   6. Momentum: multi-timeframe returns, drawdown
//
// Key differentiator from US equities: CNY exchange rate is a dominant
// macro factor for A-shares. CNY depreciation = capital outflow pressure.

package engine

import (
	"fmt"
	"math"

	"github.com/Ricaardo/guanfu/pkg/model"
)

// HS300DashboardInput holds data for the CSI300 enhanced panel.
type HS300DashboardInput struct {
	Date         string
	Price        float64
	PriceHistory []float64 // newest-first, min 200 days
	PE           float64   // from Futu snapshot or AkShare
	PB           float64
	CNYUSD       float64   // CNY/USD rate proxy (inverted UUP or direct)
	Volume       []float64 // optional
}

// BuildHS300Dashboard constructs the full CSI300 panel.
func BuildHS300Dashboard(in *HS300DashboardInput) *model.IndicatorPanel {
	if len(in.PriceHistory) < 200 {
		return nil
	}

	price := in.Price
	panel := &model.IndicatorPanel{
		Asset: "hs300",
		Date:  in.Date,
		Snapshot: model.SnapshotData{
			HS300Price: price,
			DataDate:   in.Date,
		},
		Technical:   make(map[string]model.Indicator),
		Valuation:   make(map[string]model.Indicator),
		Macro:       make(map[string]model.Indicator),
		Flow:        make(map[string]model.Indicator),
		Positioning: make(map[string]model.Indicator),
		Cycle:       make(map[string]model.Indicator),
	}

	history := in.PriceHistory

	// ─── 1. Valuation (PE Band) ───
	sma200 := calcSMA(history, 200)
	sma200Dev := (price - sma200) / sma200 * 100
	panel.Valuation["sma_200_dev"] = model.Indicator{
		Value: math.Round(sma200Dev*10) / 10,
		Label: smaDevZone(sma200Dev),
		Source: "price_store",
	}

	if in.PE > 0 {
		peZone := hs300PEZone(in.PE)
		panel.Valuation["pe"] = model.Indicator{
			Value: math.Round(in.PE*10) / 10,
			Label: peZone,
			Source: "futu:snapshot",
		}
	} else {
		panel.Valuation["pe"] = model.Indicator{
			Missing: true, Label: "待接入 (需Futu SH快照或AkShare)",
			Source: "待接入",
		}
	}

	if in.PB > 0 {
		panel.Valuation["pb"] = model.Indicator{
			Value: math.Round(in.PB*100) / 100,
			Label: fmt.Sprintf("PB %.2f", in.PB),
			Source: "futu:snapshot",
		}
	}

	// ─── 2. Technical ───
	rsi14 := calcRSIEquity(history, 14)
	macd, macdSig, macdHist := calcMACDEquity(history)
	vol30d := calcVolEquity(history, 30)

	panel.Technical["rsi_14"] = model.Indicator{
		Value: math.Round(rsi14*10) / 10,
		Label: rsiZoneLabel(rsi14),
		Source: "price_store",
	}
	panel.Technical["macd"] = model.Indicator{
		Value: math.Round(macd*10000) / 10000,
		Label: fmt.Sprintf("sig=%.4f hist=%.4f %s", macdSig, macdHist, macdLabel(macdHist)),
		Source: "price_store",
	}
	panel.Technical["volatility_30d"] = model.Indicator{
		Value: math.Round(vol30d*10) / 10,
		Label: volRegimeLabel(vol30d),
		Source: "price_store",
	}

	// ─── 3. Flow (北向资金 proxy) ───
	if len(history) >= 20 {
		volTrend := calcVolumeProxy(history, 20)
		panel.Flow["volume_trend"] = model.Indicator{
			Value: math.Round(volTrend*100) / 100,
			Label: hs300FlowLabel(volTrend, price, sma200),
			Source: "price_store",
		}
	}
	// 20d price change as northbound sentiment proxy
	mom20d := calcReturn(history, 20)
	panel.Flow["flow_sentiment"] = model.Indicator{
		Value: math.Round(mom20d*10) / 10,
		Label: hs300SentimentLabel(mom20d),
		Source: "computed",
	}

	// ─── 4. Macro (China-specific) ───
	if in.CNYUSD > 0 {
		// CNY/USD: higher = CNY weaker = A-share headwind
		cnyDev := (in.CNYUSD - 7.0) / 7.0 * 100
		panel.Macro["cny_usd"] = model.Indicator{
			Value: math.Round(in.CNYUSD*10000) / 10000,
			Label: hs300CNYLabel(cnyDev, in.CNYUSD),
			Source: "price_store",
		}
	} else {
		panel.Macro["cny_usd"] = model.Indicator{
			Missing: true, Label: "待接入 (需CNY/USD数据源)",
			Source: "待接入",
		}
	}

	// ─── 5. Momentum ───
	mom90d := calcReturn(history, 90)
	mom180d := calcReturn(history, 180)
	dd := calcMaxDrawdown(history, 200, price)

	panel.Cycle["momentum_90d"] = model.Indicator{
		Value: math.Round(mom90d*10) / 10,
		Label: momentumLabel(mom90d),
		Source: "price_store",
	}
	panel.Cycle["momentum_180d"] = model.Indicator{
		Value: math.Round(mom180d*10) / 10,
		Label: momentumLabel(mom180d),
		Source: "price_store",
	}
	panel.Cycle["drawdown_200d"] = model.Indicator{
		Value: math.Round(dd*10) / 10,
		Label: drawdownLabel(dd),
		Source: "price_store",
	}

	// ─── 6. Top/Bottom Proximity ───
	panel.StaleWarnings = append(panel.StaleWarnings,
		fmt.Sprintf("top_proximity=%.0f%% bottom_proximity=%.0f%%",
			calcHS300Top(panel)*100, calcHS300Bottom(panel)*100))

	return panel
}

// BuildHS300Verdict builds a structured verdict for CSI300.
func BuildHS300Verdict(panel *model.IndicatorPanel) *Verdict {
	v := &Verdict{
		Date:    panel.Date,
		Domains: make([]DomainVote, 0),
		Reasons: make([]string, 0),
		CounterEvidence: make([]string, 0),
	}

	// Score domains
	type domainVote struct {
		name string
		data map[string]model.Indicator
		weight float64
	}
	domains := []domainVote{
		{"valuation", panel.Valuation, 1.0},
		{"technical", panel.Technical, 1.0},
		{"momentum", panel.Cycle, 0.8},
		{"macro", panel.Macro, 1.2}, // CNY is important for A-shares
		{"flow", panel.Flow, 0.8},
	}

	netScore := 0
	for _, d := range domains {
		vote, bulls, bears := scoreHS300Domain(d.name, d.data)
		netScore += vote
		v.Domains = append(v.Domains, DomainVote{
			Domain: d.name, Vote: vote,
			Bullish: bulls, Bearish: bears,
		})
		v.Reasons = append(v.Reasons, bulls...)
		v.CounterEvidence = append(v.CounterEvidence, bears...)
	}

	v.NetDirection = netScore
	// Compute coverage
	total := 0
	avail := 0
	for _, d := range domains {
		for _, ind := range d.data {
			total++
			if ind.IsAvailable() {
				avail++
			}
		}
	}
	if total > 0 {
		v.Coverage = float64(avail) / float64(total)
	}

	switch {
	case netScore >= 2:
		v.Regime = "积累区"
		v.Stance = "估值合理或偏低+技术转强+汇率配合"
	case netScore <= -2:
		v.Regime = "谨慎区"
		v.Stance = "估值偏高或技术破位+汇率逆风"
	default:
		v.Regime = "中性"
		v.Stance = "方向不明确，等待信号确认"
	}

	v.Confidence = confidenceFromCoverage(v.Coverage)
	v.TopProximity = calcHS300Top(panel)
	v.BottomProximity = calcHS300Bottom(panel)

	return v
}

// ─── Label helpers ───

func hs300PEZone(pe float64) string {
	// CSI300 historical PE range: ~8-30, median ~13
	switch {
	case pe < 10:
		return "低估 (PE<10)"
	case pe < 13:
		return "中性偏低"
	case pe < 18:
		return "中性"
	case pe < 25:
		return "中性偏高"
	default:
		return "高估 (PE>25)"
	}
}

func hs300FlowLabel(volProxy, price, sma200 float64) string {
	dev := (price - sma200) / sma200 * 100
	switch {
	case volProxy > 3 && dev > 0:
		return "放量上涨 (资金流入)"
	case volProxy > 3 && dev < 0:
		return "放量下跌 (资金流出)"
	case volProxy < 1:
		return "缩量 (观望)"
	default:
		return "正常"
	}
}

func hs300SentimentLabel(mom20d float64) string {
	switch {
	case mom20d > 5:
		return "短期偏强 (20d动量+)"
	case mom20d < -5:
		return "短期偏弱 (20d动量-)"
	default:
		return "横盘"
	}
}

func hs300CNYLabel(dev, rate float64) string {
	switch {
	case dev > 3:
		return fmt.Sprintf("CNY贬值 %.2f (A股逆风)", rate)
	case dev < -1:
		return fmt.Sprintf("CNY升值 %.2f (A股利好)", rate)
	default:
		return fmt.Sprintf("CNY稳定 %.2f", rate)
	}
}

func calcHS300Top(panel *model.IndicatorPanel) float64 {
	s := 0.0
	c := 0
	if rsi, ok := panel.Technical["rsi_14"]; ok && rsi.IsAvailable() {
		s += math.Max(0, (rsi.Value-70)/30)
		c++
	}
	if pe, ok := panel.Valuation["pe"]; ok && pe.IsAvailable() && pe.Value > 18 {
		s += math.Min(1, (pe.Value-18)/12)
		c++
	}
	if c == 0 {
		return 0
	}
	return math.Min(1, s/float64(c))
}

func calcHS300Bottom(panel *model.IndicatorPanel) float64 {
	s := 0.0
	c := 0
	if rsi, ok := panel.Technical["rsi_14"]; ok && rsi.IsAvailable() {
		s += math.Max(0, (30-rsi.Value)/30)
		c++
	}
	if pe, ok := panel.Valuation["pe"]; ok && pe.IsAvailable() && pe.Value < 13 {
		s += math.Min(1, (13-pe.Value)/5)
		c++
	}
	if dd, ok := panel.Cycle["drawdown_200d"]; ok && dd.IsAvailable() {
		s += math.Max(0, (-dd.Value-15)/20)
		c++
	}
	if c == 0 {
		return 0
	}
	return math.Min(1, s/float64(c))
}

func scoreHS300Domain(name string, data map[string]model.Indicator) (int, []string, []string) {
	vote := 0
	var bulls, bears []string

	switch name {
	case "valuation":
		if pe, ok := data["pe"]; ok && pe.IsAvailable() {
			if pe.Value < 10 {
				bulls = append(bulls, "PE<10 历史低估区")
				vote++
			} else if pe.Value > 25 {
				bears = append(bears, "PE>25 历史高估区")
				vote--
			}
		}
	case "technical":
		if rsi, ok := data["rsi_14"]; ok && rsi.IsAvailable() {
			if rsi.Value < 30 {
				bulls = append(bulls, "RSI超卖")
				vote++
			} else if rsi.Value > 70 {
				bears = append(bears, "RSI超买")
				vote--
			}
		}
	case "momentum":
		if mom, ok := data["momentum_90d"]; ok && mom.IsAvailable() {
			if mom.Value > 10 {
				bulls = append(bulls, "90d动量转强")
				vote++
			} else if mom.Value < -10 {
				bears = append(bears, "90d动量转弱")
				vote--
			}
		}
	case "macro":
		if cny, ok := data["cny_usd"]; ok && cny.IsAvailable() {
			// Simplified: CNY rate > 7.2 = depreciation pressure
			if cny.Value < 6.8 {
				bulls = append(bulls, "CNY偏强→资本流入")
				vote++
			} else if cny.Value > 7.3 {
				bears = append(bears, "CNY偏弱→资本流出压力")
				vote--
			}
		}
	case "flow":
		if flow, ok := data["volume_trend"]; ok && flow.IsAvailable() {
			if flow.Value > 3 {
				bulls = append(bulls, "放量→资金活跃")
				vote++
			} else if flow.Value < 1 {
				bears = append(bears, "缩量→市场冷淡")
				vote--
			}
		}
	}
	return vote, bulls, bears
}



