// Portfolio-aware verdict annotation (Track L, L2/L3).
//
// Philosophy: don't change the v2 verdict contract. Instead, opt-in
// enrichment: caller has a portfolio → call AnnotateVerdictWithPortfolio
// after BuildVerdict. No portfolio → no field appears (omitempty).
//
// This lives in engine because the annotation references Verdict fields
// and is conceptually part of the verdict pipeline, but pkg/portfolio
// does the raw data decisions. One-way dependency engine → portfolio.

package engine

import (
	"fmt"
	"strings"

	"github.com/Ricaardo/guanfu/pkg/portfolio"
)

// AnnotateVerdictWithPortfolio mutates v (if non-nil) to attach
// PortfolioContext based on the user's declared holdings. Silent no-op
// when p is nil or v is nil.
//
// Inputs:
//
//	v            — verdict to annotate (mutated in place)
//	asset        — canonical asset key (btc/qqq/spy/gold/stock_xxx)
//	p            — loaded portfolio (nil if user has no portfolio.json)
//	currentPrice — current USD price of `asset` (for position value)
//	prices       — lowercase-keyed USD prices of ALL portfolio holdings
//	               (for total-portfolio weight); may be nil to skip weight
//
// The function does not do network I/O or recompute verdict direction.
// Called by CLI after BuildVerdict.
func AnnotateVerdictWithPortfolio(
	v *Verdict,
	asset string,
	p *portfolio.Portfolio,
	currentPrice float64,
	prices map[string]float64,
) {
	if v == nil || p == nil {
		return
	}
	ctx := &PortfolioContext{
		RiskBudget:   p.Preferences.RiskBudget,
		HorizonMatch: "unknown",
	}

	// Position weight (only if we have enough prices).
	if prices != nil && currentPrice > 0 {
		if _, has := p.HoldingFor(asset); has {
			if total := p.TotalValueUSD(prices); total > 0 {
				w := p.PositionValueUSD(asset, currentPrice) / total * 100
				ctx.CurrentWeightPct = round1(w)
			}
		}
	}

	ctx.CeilingPct = p.CeilingFor(asset)
	if ctx.CeilingPct > 0 && ctx.CurrentWeightPct > 0 {
		if ctx.CurrentWeightPct > ctx.CeilingPct {
			ctx.Overweight = true
			ctx.Notes = append(ctx.Notes,
				fmt.Sprintf("当前 %s 权重 %.1f%% 超出自定上限 %.0f%%；即使 verdict 偏积累，建议不追加",
					strings.ToUpper(asset), ctx.CurrentWeightPct, ctx.CeilingPct))
		} else {
			ctx.RoomToCeilingPct = round1(ctx.CeilingPct - ctx.CurrentWeightPct)
			ctx.Notes = append(ctx.Notes,
				fmt.Sprintf("距上限还有 %.1f%%，验证条件满足时仍有加仓空间", ctx.RoomToCeilingPct))
		}
	}

	// Horizon match: does the user's declared horizon align with this
	// verdict's natural time frame? A rough 3-bucket check:
	//   - 5+ years → verdict works for any stance; no mismatch
	//   - 1-5 years → ok for cycle/valuation signals; warn on overly
	//     short-term flips
	//   - <1 year → skew toward technical/positioning; long-term
	//     valuation stance may be noise
	h := p.Preferences.HorizonYears
	switch {
	case h >= 5:
		ctx.HorizonMatch = "ok"
	case h >= 1:
		ctx.HorizonMatch = "ok"
		if v.Stance != "" && strings.Contains(v.Stance, "分配") {
			ctx.Notes = append(ctx.Notes,
				"期限 "+fmt.Sprintf("%d", h)+"y 与长期分配倾向的 verdict 匹配度较低，优先读 180d+ 信号")
		}
	case h > 0:
		ctx.HorizonMatch = "mismatch"
		ctx.Notes = append(ctx.Notes,
			"你声明 horizon<1y，短期内长期估值/周期信号对你是噪声")
	default:
		ctx.HorizonMatch = "unknown"
	}

	// Risk-budget note — only add if clearly relevant to the verdict.
	if strings.Contains(v.Stance, "高防守") || strings.Contains(v.Stance, "分配") {
		if ctx.RiskBudget == "aggressive" {
			ctx.Notes = append(ctx.Notes,
				"你声明 aggressive 风险预算，但当前 verdict 偏防守 — 评估是否与自己风险偏好一致")
		}
	}
	if strings.Contains(v.Stance, "强积累") {
		if ctx.RiskBudget == "conservative" {
			ctx.Notes = append(ctx.Notes,
				"你声明 conservative 风险预算，即使 verdict 偏积累，也不必全额追进")
		}
	}

	v.PortfolioContext = ctx
}

func round1(x float64) float64 {
	return float64(int(x*10+0.5)) / 10
}
