package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	investorMacroUSDCNYKey      = "usd_cny"
	investorMacroUSDCNYTrendKey = "usd_cny_60d_trend_pct"

	investorFXSourceName          = "investor_fx"
	globalCentralBankRateSource   = "global_central_bank_rates"
	globalCentralBankRateCoverage = 4
)

// EnrichGlobalInvestorMacro adds non-asset-specific macro context to an
// existing panel. These indicators are intended for user context, especially
// non-USD investors, and are not forecast features unless explicitly wired
// into a backtested extractor bundle.
func EnrichGlobalInvestorMacro(panel *model.IndicatorPanel, ps *store.PriceStore) {
	if panel == nil || ps == nil {
		return
	}
	if panel.Macro == nil {
		panel.Macro = make(map[string]model.Indicator)
	}

	fxAsOf, fxSpotOK, fxTrendOK := addUSDCNYMacro(panel, ps)
	ratesAsOf, ratesAvailable := addCentralBankMacro(panel, ps)

	panel.SourceHealth = append(panel.SourceHealth,
		healthEntry(
			investorFXSourceName,
			combinedStatus(fxSpotOK, fxTrendOK),
			fxAsOf,
			false,
			"USD/CNY spot and 60d trend for CNY investor context",
			nil,
		),
		healthEntry(
			globalCentralBankRateSource,
			coverageStatus(ratesAvailable, globalCentralBankRateCoverage),
			ratesAsOf,
			false,
			"Fed, ECB, BOJ and China front-end rate context from refreshed macro PriceStore data",
			nil,
		),
	)
}

func addUSDCNYMacro(panel *model.IndicatorPanel, ps *store.PriceStore) (string, bool, bool) {
	pts, err := ps.Load("usd_cny")
	if err != nil || len(pts) == 0 {
		return "", false, false
	}
	latest := pts[len(pts)-1]
	panel.Macro[investorMacroUSDCNYKey] = model.Indicator{
		Value:     roundN(latest.Close, 4),
		Label:     usdCNYLevelLabel(latest.Close),
		Source:    sourceOr(latest.Source, "yahoo:CNY=X"),
		UpdatedAt: latest.Date,
		Note:      "USD/CNY spot. Higher = CNY weaker; for CNY-based investors, USD assets can rise in CNY terms even when USD price is flat.",
	}

	past, ok := pointAtLeastDaysBack(pts, latest.Date, 60)
	if !ok || past.Close <= 0 {
		return latest.Date, true, false
	}
	trend := (latest.Close - past.Close) / past.Close * 100
	panel.Macro[investorMacroUSDCNYTrendKey] = model.Indicator{
		Value:     roundN(trend, 2),
		Label:     usdCNYTrendLabel(trend),
		Source:    sourceOr(latest.Source, "yahoo:CNY=X"),
		UpdatedAt: latest.Date,
		Note:      fmt.Sprintf("USD/CNY 60d change. Reference %.4f on %s; positive = CNY depreciation / USD tailwind for CNY investors.", past.Close, past.Date),
	}
	return latest.Date, true, true
}

func addCentralBankMacro(panel *model.IndicatorPanel, ps *store.PriceStore) (string, int) {
	available := 0
	us, usAsOf, usOK := addLatestRate(panel, ps, "global_rate_us_fed_pct", "fred_fed_funds", "Fed effective rate", "fred:DFF")
	if !usOK {
		us, usAsOf, usOK = addLatestRate(panel, ps, "global_rate_us_fed_pct", "fred_dgs3mo", "US 3M T-bill proxy", "fred:DGS3MO")
	}
	eu, euAsOf, euOK := addLatestRate(panel, ps, "global_rate_eu_ecb_pct", "fred_ecb_deposit_rate", "ECB deposit rate", "fred:ECBDFR")
	jp, jpAsOf, jpOK := addLatestRate(panel, ps, "global_rate_jp_boj_pct", "fred_boj_call_rate", "BOJ overnight call rate", "fred:IRSTCI01JPM156N")
	cn, cnAsOf, cnOK := addLatestRate(panel, ps, "global_rate_cn_pboc_pct", "fred_pboc_interbank_rate", "PBoC interbank rate", "fred:IRSTCI01CNM156N")
	for _, ok := range []bool{usOK, euOK, jpOK, cnOK} {
		if ok {
			available++
		}
	}

	if usOK && cnOK {
		spread := us - cn
		panel.Macro["global_rate_spread_us_cn_pct"] = model.Indicator{
			Value:     roundN(spread, 2),
			Label:     usCNRateSpreadLabel(spread),
			Source:    "computed:fred_fed_funds-minus-fred_pboc_interbank_rate",
			UpdatedAt: latestAsOf(usAsOf, cnAsOf),
			Note:      "US policy/interbank rate minus China interbank rate. Positive spread supports USD yield advantage; read with USD/CNY trend.",
		}
	}
	if usOK && euOK && jpOK {
		avg := (us + eu + jp) / 3
		panel.Macro["global_dm_policy_rate_avg_pct"] = model.Indicator{
			Value:     roundN(avg, 2),
			Label:     globalPolicyRateLabel(avg),
			Source:    "computed:fed_ecb_boj",
			UpdatedAt: latestAsOf(usAsOf, euAsOf, jpAsOf),
			Note:      "Average of US, Euro Area and Japan front-end policy/interbank rates; context only, not a forecast trigger.",
		}
	}
	return latestAsOf(usAsOf, euAsOf, jpAsOf, cnAsOf), available
}

func addLatestRate(panel *model.IndicatorPanel, ps *store.PriceStore, indicatorKey, storeKey, labelPrefix, fallbackSource string) (float64, string, bool) {
	latest, ok := ps.Latest(storeKey)
	if !ok || latest.Close <= 0 {
		return 0, "", false
	}
	panel.Macro[indicatorKey] = model.Indicator{
		Value:     roundN(latest.Close, 3),
		Label:     fmt.Sprintf("%s %.2f%% (%s)", labelPrefix, latest.Close, policyRateLabel(latest.Close)),
		Source:    sourceOr(latest.Source, fallbackSource),
		UpdatedAt: latest.Date,
		Note:      "Global central-bank / front-end rate context. Use as macro background; do not treat as a standalone timing signal.",
	}
	return latest.Close, latest.Date, true
}

func coverageStatus(available, total int) string {
	switch {
	case total <= 0 || available <= 0:
		return "missing"
	case available >= total:
		return "ok"
	default:
		return "partial"
	}
}

func pointAtLeastDaysBack(points []store.PricePoint, latestDate string, days int) (store.PricePoint, bool) {
	if len(points) == 0 || latestDate == "" || days <= 0 {
		return store.PricePoint{}, false
	}
	target, ok := dateDaysBefore(latestDate, days)
	if !ok {
		idx := len(points) - 1 - days
		if idx >= 0 {
			return points[idx], true
		}
		return store.PricePoint{}, false
	}
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Date <= target {
			return points[i], true
		}
	}
	return store.PricePoint{}, false
}

func dateDaysBefore(date string, days int) (string, bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", false
	}
	return t.AddDate(0, 0, -days).Format("2006-01-02"), true
}

func sourceOr(source, fallback string) string {
	if source != "" {
		return source
	}
	return fallback
}

func roundN(v float64, places int) float64 {
	if places < 0 {
		return v
	}
	scale := math.Pow10(places)
	return math.Round(v*scale) / scale
}

func usdCNYLevelLabel(v float64) string {
	switch {
	case v >= 7.30:
		return "CNY weak / USD strong"
	case v >= 7.00:
		return "CNY mildly weak"
	case v >= 6.70:
		return "CNY stable"
	default:
		return "CNY strong"
	}
}

func usdCNYTrendLabel(v float64) string {
	switch {
	case v >= 3:
		return "CNY depreciation tailwind for USD assets"
	case v >= 1:
		return "mild CNY depreciation"
	case v <= -3:
		return "CNY appreciation headwind for USD assets"
	case v <= -1:
		return "mild CNY appreciation"
	default:
		return "FX roughly stable"
	}
}

func policyRateLabel(v float64) string {
	switch {
	case v >= 5:
		return "restrictive"
	case v >= 2:
		return "moderate"
	case v >= 0.5:
		return "easy"
	default:
		return "very easy"
	}
}

func usCNRateSpreadLabel(v float64) string {
	switch {
	case v >= 2:
		return "large USD yield advantage"
	case v >= 0.5:
		return "USD yield advantage"
	case v <= -1:
		return "CNY yield advantage"
	default:
		return "rate spread narrow"
	}
}

func globalPolicyRateLabel(v float64) string {
	switch {
	case v >= 4:
		return "DM policy restrictive"
	case v >= 2:
		return "DM policy moderately tight"
	case v >= 0.5:
		return "DM policy easy-normal"
	default:
		return "DM policy very easy"
	}
}
