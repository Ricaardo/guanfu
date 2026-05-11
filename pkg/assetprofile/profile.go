// Package assetprofile is the single source of asset-class policy.
//
// It deliberately avoids importing forecast or engine packages so lower-level
// packages can depend on profile data without creating import cycles.
package assetprofile

import "strings"

type AssetClass string

const (
	ClassBTC         AssetClass = "btc"
	ClassEquityIndex AssetClass = "equity_index"
	ClassGold        AssetClass = "gold"
	ClassUSStock     AssetClass = "us_stock"
)

type ReliabilityCell struct {
	DirHit float64
	NTests int
	AsOf   string
}

type Profile struct {
	Key              string
	Class            AssetClass
	DisplayName      string
	Version          string
	ReadingDomains   []DomainSpec
	Horizons         []int
	Reliability      map[int]ReliabilityCell
	ConformalScale   map[int]float64
	HorizonWeights   []HorizonWeightBand
	FeatureBundle    string
	ExpectedFeatures []string
	SkillProfileURI  string
}

type DomainSpec struct {
	Key     string
	Title   string
	Icon    string
	Purpose string
}

type HorizonWeightBand struct {
	MaxDays    int
	Multiplier map[string]float64
}

const version20260511 = "2026-05-11"

var profiles = map[string]Profile{
	"btc": {
		Key:              "btc",
		Class:            ClassBTC,
		DisplayName:      "Bitcoin",
		Version:          version20260511,
		ReadingDomains:   btcReadingDomains(),
		Horizons:         []int{30, 90, 180},
		FeatureBundle:    "btc_core",
		ExpectedFeatures: btcExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/btc",
		Reliability: map[int]ReliabilityCell{
			30:  {DirHit: 0.609, NTests: 46, AsOf: "2026-05-11"},
			90:  {DirHit: 0.609, NTests: 46, AsOf: "2026-05-11"},
			180: {DirHit: 0.630, NTests: 46, AsOf: "2026-05-11"},
		},
		HorizonWeights: defaultHorizonWeights(),
	},
	"qqq": {
		Key:              "qqq",
		Class:            ClassEquityIndex,
		DisplayName:      "Nasdaq-100 ETF",
		Version:          version20260511,
		ReadingDomains:   equityReadingDomains(),
		Horizons:         []int{30, 63, 90, 180, 252},
		FeatureBundle:    "equity_index",
		ExpectedFeatures: equityExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/equity_index",
		Reliability: map[int]ReliabilityCell{
			30:  {DirHit: 0.700, NTests: 20, AsOf: "2026-05-11"},
			90:  {DirHit: 0.750, NTests: 20, AsOf: "2026-05-11"},
			180: {DirHit: 0.800, NTests: 20, AsOf: "2026-05-11"},
		},
		ConformalScale: map[int]float64{30: 1.80, 63: 1.80, 90: 1.80, 180: 1.80, 252: 1.80},
		HorizonWeights: defaultHorizonWeights(),
	},
	"spy": {
		Key:              "spy",
		Class:            ClassEquityIndex,
		DisplayName:      "S&P 500 ETF",
		Version:          version20260511,
		ReadingDomains:   equityReadingDomains(),
		Horizons:         []int{30, 63, 90, 180, 252},
		FeatureBundle:    "equity_index",
		ExpectedFeatures: equityExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/equity_index",
		Reliability: map[int]ReliabilityCell{
			30:  {DirHit: 0.600, NTests: 20, AsOf: "2026-05-11"},
			90:  {DirHit: 0.750, NTests: 20, AsOf: "2026-05-11"},
			180: {DirHit: 0.850, NTests: 20, AsOf: "2026-05-11"},
		},
		ConformalScale: map[int]float64{30: 1.60, 63: 1.60, 90: 1.60, 180: 1.90, 252: 1.90},
		HorizonWeights: defaultHorizonWeights(),
	},
	"gold": {
		Key:              "gold",
		Class:            ClassGold,
		DisplayName:      "London Gold (XAU/USD)",
		Version:          version20260511,
		ReadingDomains:   goldReadingDomains(),
		Horizons:         []int{30, 60, 90, 120},
		FeatureBundle:    "gold",
		ExpectedFeatures: goldExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/gold",
		Reliability: map[int]ReliabilityCell{
			30: {DirHit: 0.451, NTests: 51, AsOf: "2026-05-11"},
			90: {DirHit: 0.627, NTests: 51, AsOf: "2026-05-11"},
			// 180d is retained for explicit opt-in queries even though it is
			// absent from the default Gold horizon set.
			180: {DirHit: 0.529, NTests: 51, AsOf: "2026-05-11"},
		},
		ConformalScale: map[int]float64{120: 1.20, 180: 1.20},
		HorizonWeights: defaultHorizonWeights(),
	},
	"us_stock": {
		Key:              "us_stock",
		Class:            ClassUSStock,
		DisplayName:      "US Stock",
		Version:          version20260511,
		ReadingDomains:   usStockReadingDomains(),
		Horizons:         []int{30, 90, 180},
		FeatureBundle:    "us_stock",
		ExpectedFeatures: usStockExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/us_stock",
		HorizonWeights:   defaultHorizonWeights(),
	},
}

func btcReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "cycle", Title: "Cycle 周期定位", Icon: "🌊", Purpose: "halving cycle, long-cycle trend, miner-cycle context"},
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "BTC-native valuation and on-chain valuation"},
		{Key: "network", Title: "Network 网络", Icon: "⛏️", Purpose: "hashrate, difficulty, usage, and miner stress"},
		{Key: "positioning", Title: "Positioning 杠杆 & 情绪", Icon: "📊", Purpose: "derivatives leverage, options skew, and sentiment"},
		{Key: "macro", Title: "Macro 宏观", Icon: "🌍", Purpose: "rates, dollar, liquidity, and broad risk backdrop"},
		{Key: "flow", Title: "Flow 资金流", Icon: "💸", Purpose: "ETF, stablecoin, exchange, and liquidity flow"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "trend, momentum, volatility, and drawdown state"},
		{Key: "cross_asset", Title: "CrossAsset 跨资产", Icon: "🔗", Purpose: "BTC versus gold, equities, dollar, and rates"},
	}
}

func equityReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "CAPE and broad valuation context when historical series exist"},
		{Key: "macro", Title: "Macro 利率/信用", Icon: "🌍", Purpose: "rates, dollar, credit, liquidity, and volatility backdrop"},
		{Key: "positioning", Title: "Options/Sentiment 期权情绪", Icon: "📊", Purpose: "put/call, fear-greed, and crowding context"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "trend, momentum, volatility, and drawdown state"},
	}
}

func goldReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "valuation", Title: "RealYield/USD 黄金估值", Icon: "💰", Purpose: "real-yield proxy, dollar pressure, and safe-haven valuation"},
		{Key: "macro", Title: "RiskOff/Macro 避险宏观", Icon: "🌍", Purpose: "VIX risk-off demand, dollar, and long-duration rate proxy"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "gold trend, momentum, volatility, and drawdown state"},
	}
}

func usStockReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "per-name valuation when historical or snapshot data exists"},
		{Key: "macro", Title: "Macro 宏观", Icon: "🌍", Purpose: "rates, dollar, credit, and broad risk backdrop"},
		{Key: "positioning", Title: "Flow/Sentiment 流向情绪", Icon: "📊", Purpose: "volume, options, short interest, and event-risk context when available"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "single-name trend, momentum, volatility, and drawdown state"},
	}
}

func genericTechnicalFeatures() []string {
	return []string{
		"return_30d", "return_90d", "return_180d", "drawdown_90d",
		"mayer_multiple", "realized_vol_30d", "rsi_14",
	}
}

func btcExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"sma_200w_dev", "ahr999_compressed", "halving_cycle_sin", "halving_cycle_cos")
}

func equityExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"cape", "dgs10_30d", "dxy_30d", "hy_spread_30d", "yield_curve", "vixy_level",
		"put_call_ratio", "put_call_30d_change", "put_call_252d_percentile")
}

func goldExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"real_yield_10y", "breakeven_10y", "dxy_30d", "gold_cot_net", "vixy_level")
}

func usStockExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"dgs10_30d", "dxy_30d", "hy_spread_30d", "yield_curve", "vixy_level")
}

func defaultHorizonWeights() []HorizonWeightBand {
	return []HorizonWeightBand{
		{
			MaxDays: 45,
			Multiplier: map[string]float64{
				"return_30d":               1.25,
				"drawdown_90d":             1.25,
				"realized_vol_30d":         1.25,
				"rsi_14":                   1.25,
				"vixy_level":               1.25,
				"put_call_30d_change":      1.25,
				"return_180d":              0.75,
				"cape":                     0.75,
				"ahr999_compressed":        0.75,
				"sma_200w_dev":             0.75,
				"yield_curve":              0.75,
				"put_call_252d_percentile": 0.75,
			},
		},
		{
			MaxDays: 120,
			Multiplier: map[string]float64{
				"return_90d":          1.15,
				"drawdown_90d":        1.15,
				"dgs10_30d":           1.15,
				"dxy_30d":             1.15,
				"hy_spread_30d":       1.15,
				"put_call_30d_change": 1.15,
				"return_30d":          0.90,
			},
		},
		{
			MaxDays: 0,
			Multiplier: map[string]float64{
				"return_180d":              1.25,
				"mayer_multiple":           1.25,
				"sma_200w_dev":             1.25,
				"ahr999_compressed":        1.25,
				"cape":                     1.25,
				"real_yield_10y":           1.25,
				"gold_cot_net":             1.25,
				"yield_curve":              1.25,
				"put_call_252d_percentile": 1.25,
				"return_30d":               0.75,
				"rsi_14":                   0.75,
			},
		},
	}
}

func For(asset string) (Profile, bool) {
	key := normalizeKey(asset)
	p, ok := profiles[key]
	if !ok && strings.HasPrefix(key, "stock_") {
		p, ok = profiles["us_stock"]
	}
	if !ok {
		return Profile{}, false
	}
	return cloneProfile(p), true
}

func ForClass(class AssetClass) (Profile, bool) {
	for _, p := range profiles {
		if p.Class == class {
			return cloneProfile(p), true
		}
	}
	return Profile{}, false
}

func HorizonsFor(asset string) []int {
	if p, ok := For(asset); ok {
		return append([]int(nil), p.Horizons...)
	}
	return append([]int(nil), profiles["btc"].Horizons...)
}

func ReadingDomainsFor(asset string) []DomainSpec {
	if p, ok := For(asset); ok && len(p.ReadingDomains) > 0 {
		return cloneDomainSpecs(p.ReadingDomains)
	}
	return cloneDomainSpecs(profiles["btc"].ReadingDomains)
}

func ReliabilityFor(asset string, days int) (ReliabilityCell, bool) {
	if strings.TrimSpace(asset) == "" {
		return ReliabilityCell{}, false
	}
	p, ok := For(asset)
	if !ok {
		return ReliabilityCell{}, false
	}
	r, ok := p.Reliability[days]
	return r, ok
}

func ConformalScale(asset string, days int) float64 {
	p, ok := For(asset)
	if !ok {
		return 1
	}
	if scale := p.ConformalScale[days]; scale > 0 {
		return scale
	}
	return 1
}

func ExpectedFeaturesFor(asset string) []string {
	if p, ok := For(asset); ok && len(p.ExpectedFeatures) > 0 {
		return append([]string(nil), p.ExpectedFeatures...)
	}
	return genericTechnicalFeatures()
}

func HorizonWeightMultiplier(asset, feature string, days int) float64 {
	p, ok := For(asset)
	if !ok {
		// Unknown assets still use the generic US-stock profile so callers
		// get a conservative non-BTC policy.
		p = profiles["us_stock"]
	}
	for _, band := range p.HorizonWeights {
		if band.MaxDays > 0 && days > band.MaxDays {
			continue
		}
		if v, ok := band.Multiplier[feature]; ok {
			return v
		}
		return 1
	}
	return 1
}

func normalizeKey(asset string) string {
	key := strings.ToLower(strings.TrimSpace(asset))
	if key == "" {
		return "btc"
	}
	return key
}

func cloneProfile(p Profile) Profile {
	p.ReadingDomains = cloneDomainSpecs(p.ReadingDomains)
	p.Horizons = append([]int(nil), p.Horizons...)
	p.Reliability = cloneReliability(p.Reliability)
	p.ConformalScale = cloneFloatMap(p.ConformalScale)
	p.HorizonWeights = cloneWeightBands(p.HorizonWeights)
	p.ExpectedFeatures = append([]string(nil), p.ExpectedFeatures...)
	return p
}

func cloneDomainSpecs(in []DomainSpec) []DomainSpec {
	if len(in) == 0 {
		return nil
	}
	return append([]DomainSpec(nil), in...)
}

func cloneReliability(in map[int]ReliabilityCell) map[int]ReliabilityCell {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]ReliabilityCell, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneFloatMap(in map[int]float64) map[int]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneWeightBands(in []HorizonWeightBand) []HorizonWeightBand {
	out := make([]HorizonWeightBand, len(in))
	for i, band := range in {
		out[i] = HorizonWeightBand{MaxDays: band.MaxDays, Multiplier: map[string]float64{}}
		for k, v := range band.Multiplier {
			out[i].Multiplier[k] = v
		}
	}
	return out
}
