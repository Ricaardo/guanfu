// Regime detection — rule-based market state classification.
//
// Three regimes:
//   - Trend Bull: price > 200SMA, SMA rising, RSI > 50, Mayer > 1.0
//   - Trend Bear: price < 200SMA, SMA declining, RSI < 50, Mayer < 1.0
//   - Fracture:   extreme vol, large drawdown, RSI extreme, Mayer < 0.5
//
// This is a qualitative state classifier, not a trading signal.

package forecast

import (
	"fmt"
	"math"
)

// RegimeLabel identifies the market state.
type RegimeLabel string

const (
	RegimeBull     RegimeLabel = "trend_bull"
	RegimeBear     RegimeLabel = "trend_bear"
	RegimeFracture RegimeLabel = "fracture"
	RegimeUnknown  RegimeLabel = "unknown"
)

// RegimeResult holds the regime classification with evidence.
type RegimeResult struct {
	Label    RegimeLabel `json:"label"`
	Desc     string      `json:"desc"`
	Score    float64     `json:"score"`    // -1.0 (bear) to +1.0 (bull)
	Evidence []string    `json:"evidence"`
}

// DetectRegime classifies the current market state from price history.
func DetectRegime(points []Point) *RegimeResult {
	if len(points) < 200 {
		return &RegimeResult{Label: RegimeUnknown, Desc: "insufficient history"}
	}

	i := len(points) - 1 // current index
	price := points[i].Close

	// 200d SMA
	sma200, _ := sma200Regime(points, i)
	sma50, _ := sma50Regime(points, i)
	mayer := mayerRegime(points, i)
	rsi, _ := rsiRegime(points, i, 14)
	vol, _ := volRegime(points, i, 30)
	dd, _ := ddRegime(points, i, 90)

	score := 0.0
	var evidence []string

	// SMA direction
	if price > sma200 && sma200 > 0 {
		score += 0.3
		evidence = append(evidence, fmt.Sprintf("Price > 200SMA ($%.0f)", sma200))
	} else if sma200 > 0 {
		score -= 0.3
		evidence = append(evidence, fmt.Sprintf("Price < 200SMA ($%.0f)", sma200))
	}

	// 50 vs 200 SMA
	if sma50 > sma200 && sma50 > 0 && sma200 > 0 {
		score += 0.2
		evidence = append(evidence, "50SMA > 200SMA (golden cross)")
	} else if sma50 > 0 && sma200 > 0 {
		score -= 0.2
		evidence = append(evidence, "50SMA < 200SMA (death cross)")
	}

	// Mayer Multiple
	if mayer > 1.5 {
		score += 0.2
		evidence = append(evidence, fmt.Sprintf("Mayer %.2f (extended)", mayer))
	} else if mayer < 0.8 {
		score -= 0.2
		evidence = append(evidence, fmt.Sprintf("Mayer %.2f (depressed)", mayer))
	}

	// RSI
	if rsi > 60 {
		score += 0.15
	} else if rsi < 40 {
		score -= 0.15
		evidence = append(evidence, fmt.Sprintf("RSI %.0f", rsi))
	}

	// Volatility
	if vol > 1.0 {
		score -= 0.15
		evidence = append(evidence, fmt.Sprintf("High vol %.0f%%", vol*100))
	}

	// Drawdown
	if dd < -0.30 {
		score -= 0.2
		evidence = append(evidence, fmt.Sprintf("Large DD %.0f%%", dd*100))
	}

	// Classify
	var label RegimeLabel
	var desc string
	switch {
	case mayer < 0.5 && dd < -0.40:
		label = RegimeFracture
		desc = "断裂：极端估值+大幅回撤，历史性底部区域或系统性风险"
	case score > 0.4:
		label = RegimeBull
		desc = "趋势牛：价格高于200SMA，均线多头排列，动量偏强"
	case score < -0.4:
		label = RegimeBear
		desc = "趋势熊：价格低于200SMA，均线空头排列，动量偏弱"
	default:
		if score > 0 {
			label = RegimeBull
			desc = "偏强震荡：技术面略偏多但信号不够强"
		} else {
			label = RegimeBear
			desc = "偏弱震荡：技术面略偏空但未到趋势熊"
		}
	}

	return &RegimeResult{
		Label:    label,
		Desc:     desc,
		Score:    math.Round(score*100) / 100,
		Evidence: evidence,
	}
}

// ─── Helpers ────────────────────────────────────────────

func sma200Regime(points []Point, i int) (float64, bool) {
	if i < 199 {
		return 0, false
	}
	sum := 0.0
	for j := i - 199; j <= i; j++ {
		sum += points[j].Close
	}
	return sum / 200, true
}

func sma50Regime(points []Point, i int) (float64, bool) {
	if i < 49 {
		return 0, false
	}
	sum := 0.0
	for j := i - 49; j <= i; j++ {
		sum += points[j].Close
	}
	return sum / 50, true
}

func mayerRegime(points []Point, i int) float64 {
	sma, ok := sma200Regime(points, i)
	if !ok || sma <= 0 {
		return 1.0
	}
	return points[i].Close / sma
}

func rsiRegime(points []Point, i, w int) (float64, bool) {
	if i < w {
		return 50, false
	}
	gains, losses := 0.0, 0.0
	for j := i - w + 1; j <= i; j++ {
		diff := points[j].Close - points[j-1].Close
		if diff > 0 {
			gains += diff
		} else {
			losses -= diff
		}
	}
	if losses == 0 {
		return 100, true
	}
	rs := (gains / float64(w)) / (losses / float64(w))
	return 100 - 100/(1+rs), true
}

func volRegime(points []Point, i, w int) (float64, bool) {
	if i < w {
		return 0, false
	}
	returns := make([]float64, w)
	for j := i - w + 1; j <= i; j++ {
		returns[j-(i-w+1)] = math.Log(points[j].Close / points[j-1].Close)
	}
	std := stddevRegime(returns)
	return std * math.Sqrt(365), true
}

func ddRegime(points []Point, i, w int) (float64, bool) {
	if i < w {
		return 0, false
	}
	peak := 0.0
	for j := i - w + 1; j <= i; j++ {
		if points[j].Close > peak {
			peak = points[j].Close
		}
	}
	if peak <= 0 {
		return 0, false
	}
	return points[i].Close/peak - 1, true
}

func stddevRegime(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	variance := 0.0
	for _, v := range vals {
		variance += (v - mean) * (v - mean)
	}
	return math.Sqrt(variance / float64(len(vals)))
}
