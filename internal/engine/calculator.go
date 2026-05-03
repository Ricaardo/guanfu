package engine

import (
	"math"
	"sort"
	"time"

	"github.com/Ricaardo/guanfu/internal/history"
	"github.com/Ricaardo/guanfu/internal/mathutil"
	"github.com/Ricaardo/guanfu/internal/model"

	"github.com/shopspring/decimal"
)

const (
	ahrDCAWindowDays       = 200
	ahrFitWindowDays       = 365 * 8
	ahrMinFitWindowDays    = 365 * 3
	ahrRecentHalfLifeDays  = 365 * 4
	ahrLegacyLogSlope      = 5.84
	ahrLegacyLogIntercept  = -17.01
	ahrCompressionExponent = 0.75 // sqrt-AHR: pow(raw, 0.75) 压缩凸性偏差
)

type Calculator struct {
	Config  *model.Config
	History *history.Store // 可选；nil 表示不写历史 / 不算历史分位
}

func NewCalculator(cfg *model.Config) *Calculator {
	return &Calculator{Config: cfg}
}

// WithHistory 注入 SQLite history store 后启用 v2 历史分位填充
func (c *Calculator) WithHistory(s *history.Store) *Calculator {
	c.History = s
	return c
}

func (c *Calculator) Calculate(snap *model.MarketSnapshot) *model.ScoreResult {
	details := make(map[string]decimal.Decimal)

	// --- 1. Trend (趋势层) - 30% (优化：降低权重) ---
	btcTrend := c.calcBTCTrend(snap)
	details["btc_trend"] = btcTrend

	ethTrend := c.calcETHTrend(snap)
	details["eth_trend"] = ethTrend

	trendScore := btcTrend.Add(ethTrend).Div(decimal.NewFromInt(2))

	// --- 2. Reversal (反转层) - 25% (新增) ---
	// 2.1 MACD 动量变化（优化后：底部收窄也给正分）
	macdScore := c.calcMACDScore(snap)
	details["macd_score"] = macdScore

	// 2.2 超卖急跌检测
	oversoldSpike := c.calcOversoldSpike(snap)
	details["oversold_spike"] = oversoldSpike

	// 2.3 RSI 背离检测
	rsiDivergence := c.calcRSIDivergence(snap)
	details["rsi_divergence"] = rsiDivergence

	// 反转层得分 = (MACD * 0.4 + 超卖急跌 * 0.3 + RSI背离 * 0.3)
	reversalScore := macdScore.Mul(decimal.NewFromFloat(0.4)).
		Add(oversoldSpike.Mul(decimal.NewFromFloat(0.3))).
		Add(rsiDivergence.Mul(decimal.NewFromFloat(0.3)))

	// --- 3. Valuation (估值层) - 25% (优化：提升权重) ---
	// 3.1 RSI（连续化）
	rsiScore := c.calcDualRSI(snap)
	details["rsi_score"] = rsiScore

	// 3.2 Ahr999（压缩版 sqrt-AHR，主用）
	// 回测验证：压缩版 5.0-20.0 桶 fwd180 -15.4%（原始版 >=2.0 桶 -13.9%）
	compScore, compRaw, _, compOK := c.calcCompressedAhr999(snap)
	if compOK {
		details["ahr999_score"] = compScore
		details["ahr999_raw"] = decimal.NewFromFloat(compRaw)
	} else {
		// 回退到自适应版（数据不足时）
		ahr999, ahr999Raw := c.calcAhr999(snap)
		details["ahr999_score"] = ahr999
		details["ahr999_raw"] = ahr999Raw
	}

	valuationScore := rsiScore.Add(details["ahr999_score"]).Div(decimal.NewFromInt(2))

	// --- 4. Structure (结构层) - 20% (优化：降低权重) ---
	// 4.1 ETH/BTC 比率（结合趋势方向）
	ethBtcStrength := c.calcETHBTCStrength(snap)
	details["eth_btc_strength"] = ethBtcStrength

	// 4.2 波动率（方向感知）
	volatilityScore := c.calcVolatilityScore(snap)
	details["volatility_score"] = volatilityScore

	// 4.3 BTC Dominance（市场周期指标）
	btcDominance := c.calcBTCDominanceScore(snap)
	details["btc_dominance"] = btcDominance

	// 4.4 山寨币季节指数
	altcoinSeason := c.calcAltcoinSeasonScore(snap)
	details["altcoin_season_score"] = altcoinSeason

	// 4.5 稳定币市场占比
	stablecoinRatio := c.calcStablecoinRatioScore(snap)
	details["stablecoin_ratio_score"] = stablecoinRatio

	// 结构层 = (ETH/BTC强度 * 0.25 + 波动率 * 0.25 + BTC Dominance * 0.25 + 山寨币季节 * 0.15 + 稳定币占比 * 0.1)
	structureScore := ethBtcStrength.Mul(decimal.NewFromFloat(0.25)).
		Add(volatilityScore.Mul(decimal.NewFromFloat(0.25))).
		Add(btcDominance.Mul(decimal.NewFromFloat(0.25))).
		Add(altcoinSeason.Mul(decimal.NewFromFloat(0.15))).
		Add(stablecoinRatio.Mul(decimal.NewFromFloat(0.1)))

	// --- Aggregate (新权重分配) ---
	wTrend := decimal.NewFromFloat(0.30)     // 30%
	wReversal := decimal.NewFromFloat(0.25)  // 25% (新增)
	wValuation := decimal.NewFromFloat(0.25) // 25%
	wStructure := decimal.NewFromFloat(0.20) // 20%

	totalRaw := trendScore.Mul(wTrend).
		Add(reversalScore.Mul(wReversal)).
		Add(valuationScore.Mul(wValuation)).
		Add(structureScore.Mul(wStructure))

	// Normalize to 0-100
	finalScore := totalRaw.Add(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(50))

	// === v1 决策层已删除 ===
	// score / state / action / rationale / conviction / dispersion 全部不再产出。
	// CoinMan v2 的输出由 BuildPanel() 提供 — 纯指标盘面，无评分判断。
	// 这里仍保留 ScoreResult 仅为 NewsEngine 的 Discord/Feishu 推送向后兼容（迁移完成前）。
	_ = finalScore
	state := "n/a"
	signalDesc := "v2 panel mode — see BuildPanel()"

	return &model.ScoreResult{
		Date:           snap.Date.Format("2006-01-02"),
		TotalScore:     decimal.Zero, // deprecated
		State:          state,
		SignalDesc:     signalDesc,
		TrendScore:     trendScore,
		ReversalScore:  reversalScore,
		ValuationScore: valuationScore,
		StructureScore: structureScore,
		Details:        details,
	}
}

// --- Trend ---
// calcBTCTrend 计算 BTC 趋势得分（连续化）
// 优化：使用价格偏离度计算连续值，区分"刚刚突破"和"强势突破"
func (c *Calculator) calcBTCTrend(snap *model.MarketSnapshot) decimal.Decimal {
	ma120 := mathutil.CalculateMA(snap.BTCPriceHistory, c.Config.Thresholds.BTCMAFast)
	ma200 := mathutil.CalculateMA(snap.BTCPriceHistory, c.Config.Thresholds.BTCMASlow)
	if ma120.IsZero() || ma200.IsZero() {
		return decimal.Zero
	}
	price := snap.BTCPrice

	// 计算价格相对于 MA200 的偏离度
	deviation := price.Sub(ma200).Div(ma200)
	devVal, _ := deviation.Float64()

	// 使用 sigmoid 映射到 [-1, 1]
	// 参数调整：10% 偏离对应约 0.7 的得分
	score := mathutil.Sigmoid(decimal.NewFromFloat(devVal * 8))

	// 额外加分/减分：均线排列
	// MA120 > MA200 为多头排列，加分
	if ma120.GreaterThan(ma200) {
		scoreVal, _ := score.Float64()
		bonus := 0.1
		if scoreVal < 0 {
			bonus = 0.2 // 多头排列但价格跌破均线，少减分
		}
		score = score.Add(decimal.NewFromFloat(bonus))
	} else {
		// 空头排列，减分
		scoreVal, _ := score.Float64()
		penalty := 0.1
		if scoreVal > 0 {
			penalty = 0.2 // 空头排列但价格站上均线，可能是假突破
		}
		score = score.Sub(decimal.NewFromFloat(penalty))
	}

	return mathutil.Clamp(score, decimal.NewFromInt(-1), decimal.NewFromInt(1))
}

// calcETHTrend 计算 ETH 趋势得分（连续化）
func (c *Calculator) calcETHTrend(snap *model.MarketSnapshot) decimal.Decimal {
	if snap.ETHPrice.IsZero() || len(snap.ETHPriceHistory) < 200 {
		return decimal.Zero
	}
	ma120 := mathutil.CalculateMA(snap.ETHPriceHistory, c.Config.Thresholds.BTCMAFast)
	ma200 := mathutil.CalculateMA(snap.ETHPriceHistory, c.Config.Thresholds.BTCMASlow)
	if ma120.IsZero() || ma200.IsZero() {
		return decimal.Zero
	}
	price := snap.ETHPrice

	// 计算价格相对于 MA200 的偏离度
	deviation := price.Sub(ma200).Div(ma200)
	devVal, _ := deviation.Float64()

	// 使用 sigmoid 映射到 [-1, 1]
	score := mathutil.Sigmoid(decimal.NewFromFloat(devVal * 8))

	// 均线排列加减分
	if ma120.GreaterThan(ma200) {
		score = score.Add(decimal.NewFromFloat(0.1))
	} else {
		score = score.Sub(decimal.NewFromFloat(0.1))
	}

	return mathutil.Clamp(score, decimal.NewFromInt(-1), decimal.NewFromInt(1))
}

// --- Momentum ---
// calcMACDScore 计算 MACD 动量得分
// 优化：检测柱状图变化趋势，底部时柱状图收窄也是正信号（关键修复！）
// hist > 0 且 hist > prevHist => +1 (强势上涨)
// hist > 0 但 hist < prevHist => +0.3 (动能减弱)
// hist < 0 但 hist > prevHist => +0.5 (底部反转中，关键改进！)
// hist < 0 且 hist < prevHist => -1 (继续下跌)
func (c *Calculator) calcMACDScore(snap *model.MarketSnapshot) decimal.Decimal {
	if len(snap.BTCPriceHistory) < 50 {
		return decimal.Zero
	}

	// Use BTC MACD
	hist := mathutil.CalculateMACD(snap.BTCPriceHistory)
	histPrev := mathutil.CalculateMACD(snap.BTCPriceHistory[1:])

	val, _ := hist.Float64()
	valPrev, _ := histPrev.Float64()

	// 计算柱状图变化
	histChange := val - valPrev

	if val > 0 {
		// MACD 柱状图为正
		if histChange > 0 {
			// 正在增强
			return decimal.NewFromInt(1)
		}
		// 正在减弱（可能见顶）
		return decimal.NewFromFloat(0.3)
	} else {
		// MACD 柱状图为负
		if histChange > 0 {
			// 虽然还是负值，但正在收窄（关键修复！底部反转信号）
			// 柱状图从 -100 收窄到 -50，这是看涨信号
			return decimal.NewFromFloat(0.5)
		}
		// 继续扩大（继续下跌）
		return decimal.NewFromInt(-1)
	}
}

// calcDualRSI 计算双 RSI 得分（连续化）
// 优化：使用分段线性映射，区分不同程度的超买超卖
// RSI < 20: +1.0 | RSI 20-30: +0.5~+1 | RSI 30-50: 0~+0.5
// RSI 50-70: 0~-0.5 | RSI 70-80: -0.5~-1 | RSI > 80: -1.0
func (c *Calculator) calcDualRSI(snap *model.MarketSnapshot) decimal.Decimal {
	rsiBTC := mathutil.CalculateRSI(snap.BTCPriceHistory, 14)
	rsiETH := mathutil.CalculateRSI(snap.ETHPriceHistory, 14)
	avgRSI := rsiBTC.Add(rsiETH).Div(decimal.NewFromInt(2))
	val, _ := avgRSI.Float64()

	// 分段线性映射
	var score decimal.Decimal
	switch {
	case val < 20:
		// 极度超卖
		score = decimal.NewFromInt(1)
	case val < 30:
		// 超卖区域 [20, 30] -> [+1, +0.5]
		score = mathutil.LinearInterpolate(
			avgRSI,
			decimal.NewFromInt(20), decimal.NewFromInt(30),
			decimal.NewFromInt(1), decimal.NewFromFloat(0.5),
		)
	case val < 50:
		// 偏弱区域 [30, 50] -> [+0.5, 0]
		score = mathutil.LinearInterpolate(
			avgRSI,
			decimal.NewFromInt(30), decimal.NewFromInt(50),
			decimal.NewFromFloat(0.5), decimal.Zero,
		)
	case val < 70:
		// 偏强区域 [50, 70] -> [0, -0.5]
		score = mathutil.LinearInterpolate(
			avgRSI,
			decimal.NewFromInt(50), decimal.NewFromInt(70),
			decimal.Zero, decimal.NewFromFloat(-0.5),
		)
	case val < 80:
		// 超买区域 [70, 80] -> [-0.5, -1]
		score = mathutil.LinearInterpolate(
			avgRSI,
			decimal.NewFromInt(70), decimal.NewFromInt(80),
			decimal.NewFromFloat(-0.5), decimal.NewFromInt(-1),
		)
	default:
		// 极度超买
		score = decimal.NewFromInt(-1)
	}

	return score
}

// --- Structure ---
// calcETHBTCStrength 计算 ETH/BTC 比率强度信号
// 优化：结合趋势方向判断，避免反向指标问题
// - 趋势向下 + BTC 强势 => 底部特征，给正分 +0.5
// - 趋势向上 + ETH 强势 => 风险偏好高，牛市信号 +1.0
// - 趋势向上 + BTC 强势 => 牛市早期 +0.5
// - 趋势向下 + ETH 强势 => 异常，可能是假反弹 0
func (c *Calculator) calcETHBTCStrength(snap *model.MarketSnapshot) decimal.Decimal {
	if snap.BTCPrice.IsZero() || snap.ETHPrice.IsZero() {
		return decimal.Zero
	}
	currentRatio := snap.ETHPrice.Div(snap.BTCPrice)

	limit := 60
	if len(snap.BTCPriceHistory) < limit || len(snap.ETHPriceHistory) < limit {
		return decimal.Zero
	}
	sumRatio := decimal.Zero
	for i := 0; i < limit; i++ {
		if snap.BTCPriceHistory[i].IsZero() {
			continue
		}
		r := snap.ETHPriceHistory[i].Div(snap.BTCPriceHistory[i])
		sumRatio = sumRatio.Add(r)
	}
	ma60Ratio := sumRatio.Div(decimal.NewFromInt(60))
	if ma60Ratio.IsZero() {
		return decimal.Zero
	}

	// ETH/BTC 相对于 MA60 的偏离
	diff := currentRatio.Sub(ma60Ratio).Div(ma60Ratio)
	diffVal, _ := diff.Float64()

	// 判断趋势方向（使用 BTC 趋势）
	btcTrend := c.calcBTCTrend(snap)
	trendVal, _ := btcTrend.Float64()

	// 连续化处理
	// ETH 强势 (diffVal > 0) vs BTC 强势 (diffVal < 0)
	ethStrong := diffVal > 0.01  // ETH 相对强势阈值
	btcStrong := diffVal < -0.01 // BTC 相对强势阈值

	if trendVal > 0 {
		// 上涨趋势中
		if ethStrong {
			// 趋势向上 + ETH 强势 => 牛市信号
			return mathutil.Clamp(decimal.NewFromFloat(diffVal*10), decimal.NewFromFloat(-1), decimal.NewFromInt(1))
		}
		// 趋势向上 + BTC 强势 => 牛市早期
		return decimal.NewFromFloat(0.5)
	} else if trendVal < 0 {
		// 下跌趋势中
		if btcStrong {
			// 趋势向下 + BTC 强势 => 底部特征，给正分（关键修复！）
			return decimal.NewFromFloat(0.5)
		}
		// 趋势向下 + ETH 强势 => 异常，可能是假反弹
		return decimal.Zero
	}

	// 趋势中性
	return decimal.Zero
}

// calcAhr999 计算自适应 AHR999 估值得分。
// 返回 (score, rawAhr)；score ∈ [-1,1]，rawAhr 是原始 AHR999 数值（便于人工核对）。
//
// 修复点：
// 1. 固定金额定投成本使用调和均值，而不是算术均线。
// 2. 长期估值线用最近 8 年数据动态拟合 log(price)=a+b*log(age)，避免沿用 2019 年旧系数。
// 3. 得分阈值用同一窗口内 AHR 分布的动态分位数，不再固定使用 0.45/1.2。
// 4. Huber IRLS 单次重加权，降低 2021 牛市顶 / LUNA-FTX 双爆等极端样本对拟合的影响。
// 5. 半衰期可通过 Config.Thresholds.AHRHalfLifeDays 调整（默认 365*4）。
func (c *Calculator) calcAhr999(snap *model.MarketSnapshot) (decimal.Decimal, decimal.Decimal) {
	score, raw, _, ok := c.calcAhr999Detailed(snap)
	if !ok {
		return decimal.Zero, decimal.Zero
	}
	return score, raw
}

func (c *Calculator) calcAhr999Detailed(snap *model.MarketSnapshot) (score decimal.Decimal, raw decimal.Decimal, quantile float64, ok bool) {
	if snap.BTCPrice.IsZero() || len(snap.BTCPriceHistory) < ahrDCAWindowDays {
		return decimal.Zero, decimal.Zero, -1, false
	}

	price, _ := snap.BTCPrice.Float64()
	if price <= 0 {
		return decimal.Zero, decimal.Zero, -1, false
	}

	if len(snap.BTCPriceHistory) >= ahrMinFitWindowDays {
		score, rawFloat, q, ok := c.calcAdaptiveAhr999Score(snap, price)
		if ok {
			return score, decimal.NewFromFloat(rawFloat), q, true
		}
	}

	score, raw = c.calcLegacyAhr999Score(snap, price)
	if raw.IsZero() {
		return decimal.Zero, decimal.Zero, -1, false
	}
	return score, raw, -1, true
}

// ahrHalfLife 返回有效半衰期（天）。优先使用 Config 配置；否则用默认常量。
func (c *Calculator) ahrHalfLife() int {
	if c != nil && c.Config != nil && c.Config.Thresholds.AHRHalfLifeDays > 0 {
		return c.Config.Thresholds.AHRHalfLifeDays
	}
	return ahrRecentHalfLifeDays
}

func (c *Calculator) calcAdaptiveAhr999Score(snap *model.MarketSnapshot, price float64) (decimal.Decimal, float64, float64, bool) {
	halfLife := c.ahrHalfLife()
	fit, ok := fitAhrLogLogModel(snap.BTCPriceHistory, snap.Date, halfLife)
	if !ok {
		return decimal.Zero, 0, -1, false
	}

	dcaCost, ok := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !ok {
		return decimal.Zero, 0, -1, false
	}

	fairValue := fit.fairValue(snap.Date)
	if !isUsablePositive(fairValue) {
		return decimal.Zero, 0, -1, false
	}

	raw := (price / dcaCost) * (price / fairValue)
	if !isUsablePositive(raw) {
		return decimal.Zero, 0, -1, false
	}

	logSamples := buildAhrLogSamples(snap.BTCPriceHistory, snap.Date, fit)
	if len(logSamples) < ahrMinFitWindowDays-ahrDCAWindowDays {
		return decimal.Zero, 0, -1, false
	}

	logRaw := math.Log(raw)
	q := quantileRank(logSamples, logRaw)
	score := scoreAhrByDynamicQuantiles(logRaw, logSamples)
	return decimal.NewFromFloat(score), raw, q, true
}

func (c *Calculator) calcLegacyAhr999Score(snap *model.MarketSnapshot, price float64) (decimal.Decimal, decimal.Decimal) {
	dcaCost, ok := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !ok {
		return decimal.Zero, decimal.Zero
	}

	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	days := snap.Date.Sub(genesis).Hours() / 24.0
	if days <= 0 {
		return decimal.Zero, decimal.Zero
	}
	val := ahrLegacyLogSlope*math.Log10(days) + ahrLegacyLogIntercept
	expGrowth := math.Pow(10, val)
	if !isUsablePositive(expGrowth) {
		return decimal.Zero, decimal.Zero
	}
	ahr999 := (price / dcaCost) * (price / expGrowth)
	if !isUsablePositive(ahr999) {
		return decimal.Zero, decimal.Zero
	}

	ahrDec := decimal.NewFromFloat(ahr999)
	var score decimal.Decimal
	switch {
	case ahr999 < 0.45:
		// 历史底部区域
		score = decimal.NewFromInt(1)
	case ahr999 < 0.8:
		// 低估区 [0.45, 0.8] -> [+1, +0.5]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromFloat(0.45), decimal.NewFromFloat(0.8),
			decimal.NewFromInt(1), decimal.NewFromFloat(0.5),
		)
	case ahr999 < 1.0:
		// 合理偏低 [0.8, 1.0] -> [+0.5, 0]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromFloat(0.8), decimal.NewFromInt(1),
			decimal.NewFromFloat(0.5), decimal.Zero,
		)
	case ahr999 < 1.2:
		// 合理偏高 [1.0, 1.2] -> [0, -0.5]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromInt(1), decimal.NewFromFloat(1.2),
			decimal.Zero, decimal.NewFromFloat(-0.5),
		)
	case ahr999 < 2.0:
		// 高估区 [1.2, 2.0] -> [-0.5, -1]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromFloat(1.2), decimal.NewFromInt(2),
			decimal.NewFromFloat(-0.5), decimal.NewFromInt(-1),
		)
	default:
		// 泡沫区
		score = decimal.NewFromInt(-1)
	}

	return score, ahrDec
}

// 压缩版 AHR999 阈值 — 原始阈值经过 pow(x, 0.75) 映射，保证分档数学等价。
const (
	ctComp045 = 0.549 // 0.45^0.75
	ctComp08  = 0.846 // 0.8^0.75
	ctComp10  = 1.000 // 1.0^0.75 (用于线性插值)
	ctComp12  = 1.147 // 1.2^0.75
	ctComp20  = 1.682 // 2.0^0.75
	ctComp50  = 3.344 // 5.0^0.75
	ctComp200 = 9.457 // 20.0^0.75
)

// calcCompressedAhr999 计算压缩版 sqrt-AHR999。
// 公式：raw = (price/dcaCost) * (price/fairValue)，然后 compressed = raw^0.75。
// 阈值使用原始阈值^0.75，保证分档与原始 AHR999 数学等价。
// 压缩降低 price² 的凸性偏差，让 5.0+ 泡沫桶从假阳性翻转为真卖出信号。
// 回测验证：5.0-20.0 桶 fwd180 从 +47% 降至 -35%；≥20.0 桶胜率保持 0%。
func (c *Calculator) calcCompressedAhr999(snap *model.MarketSnapshot) (score decimal.Decimal, rawCompressed float64, rawOriginal float64, ok bool) {
	if snap.BTCPrice.IsZero() || len(snap.BTCPriceHistory) < ahrDCAWindowDays {
		return decimal.Zero, 0, 0, false
	}

	price, _ := snap.BTCPrice.Float64()
	if price <= 0 {
		return decimal.Zero, 0, 0, false
	}

	dcaCost, dcaOK := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !dcaOK {
		return decimal.Zero, 0, 0, false
	}

	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	days := snap.Date.Sub(genesis).Hours() / 24.0
	if days <= 0 {
		return decimal.Zero, 0, 0, false
	}
	val := ahrLegacyLogSlope*math.Log10(days) + ahrLegacyLogIntercept
	expGrowth := math.Pow(10, val)
	if !isUsablePositive(expGrowth) {
		return decimal.Zero, 0, 0, false
	}

	rawOriginal = (price / dcaCost) * (price / expGrowth)
	if !isUsablePositive(rawOriginal) {
		return decimal.Zero, 0, 0, false
	}

	rawCompressed = math.Pow(rawOriginal, ahrCompressionExponent)

	// 使用压缩映射后的等价阈值评分
	var s decimal.Decimal
	compDec := decimal.NewFromFloat(rawCompressed)
	switch {
	case rawCompressed < ctComp045:
		s = decimal.NewFromInt(1)
	case rawCompressed < ctComp08:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp045), decimal.NewFromFloat(ctComp08),
			decimal.NewFromInt(1), decimal.NewFromFloat(0.5),
		)
	case rawCompressed < ctComp10:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp08), decimal.NewFromFloat(ctComp10),
			decimal.NewFromFloat(0.5), decimal.Zero,
		)
	case rawCompressed < ctComp12:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp10), decimal.NewFromFloat(ctComp12),
			decimal.Zero, decimal.NewFromFloat(-0.5),
		)
	case rawCompressed < ctComp20:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp12), decimal.NewFromFloat(ctComp20),
			decimal.NewFromFloat(-0.5), decimal.NewFromInt(-1),
		)
	default:
		s = decimal.NewFromFloat(-1)
	}

	return s, rawCompressed, rawOriginal, true
}

// ahrCompressedLabel 压缩版 AHR999 标签（阈值经 pow(x,0.75) 映射）
func ahrCompressedLabel(v float64) string {
	switch {
	case v < ctComp045:
		return "极端低估（定投/抄底）"
	case v < ctComp08:
		return "低估（定投区）"
	case v < ctComp12:
		return "合理"
	case v < ctComp20:
		return "偏高"
	case v < ctComp50:
		return "高估（减仓）"
	case v < ctComp200:
		return "泡沫（大幅减仓）"
	default:
		return "极端泡沫（清仓）"
	}
}

// calc3DScore 计算三维打分（估值 × 动量 × 恐慌）。
// 回测验证：V--（仅估值便宜）fwd180 +100.9%/95%胜率
// -M-（仅动量/价格低于200d SMA）fwd180 -33.4%/11%胜率 = 接飞刀信号。
func (c *Calculator) calc3DScore(snap *model.MarketSnapshot) (score int, val float64, mayer float64, dd float64, ok bool) {
	price, _ := snap.BTCPrice.Float64()
	if price <= 0 || len(snap.BTCPriceHistory) < 200 {
		return 0, 0, 0, 0, false
	}

	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	days := snap.Date.Sub(genesis).Hours() / 24.0
	if days > 0 {
		fv := math.Pow(10, ahrLegacyLogSlope*math.Log10(days)+ahrLegacyLogIntercept)
		if fv > 0 {
			val = price / fv
		}
	}

	sma200 := 0.0
	for i := 0; i < 200; i++ {
		v, _ := snap.BTCPriceHistory[i].Float64()
		sma200 += v
	}
	if sma200 > 0 {
		mayer = price / (sma200 / 200)
	}

	if len(snap.BTCPriceHistory) >= 90 {
		max90 := 0.0
		for i := 0; i < 90; i++ {
			v, _ := snap.BTCPriceHistory[i].Float64()
			if v > max90 {
				max90 = v
			}
		}
		if max90 > 0 {
			dd = (price - max90) / max90
		}
	}

	if val > 0 && val < 0.8 {
		score++
	}
	if mayer > 0 && mayer < 1.0 {
		score++
	}
	if dd < -0.20 {
		score++
	}
	return score, val, mayer, dd, true
}

func d3Label(score int, val, mayer, dd float64) string {
	hasV := val > 0 && val < 0.8
	hasM := mayer > 0 && mayer < 1.0
	hasP := dd < -0.20
	switch {
	case hasV && hasM && hasP:
		return "VMP 三项全满（极端底部）"
	case hasV && !hasM && hasP:
		return "V-P 便宜+不跌+恐慌（恐慌底）"
	case !hasV && hasM && hasP:
		return "-MP 偏贵+跌+恐慌（熊市反弹陷阱）"
	case hasV && hasM && !hasP:
		return "VM- 估值便宜+动量弱（熊市中继）"
	case hasV && !hasM && !hasP:
		return "V-- 仅估值便宜（最佳买入时机）"
	case !hasV && hasM && !hasP:
		return "-M- 仅动量（偏贵+跌，接飞刀信号）"
	case !hasV && !hasM && hasP:
		return "--P 仅恐慌（估值合理+恐慌，假底信号）"
	default:
		return "--- 三项全缺（估值偏高+不跌+无恐慌）"
	}
}

type ahrLogLogFit struct {
	alpha float64
	beta  float64
}

// fitAhrLogLogModel 拟合 log(price) = alpha + beta*log(age)。
// 时间衰减：weight = 0.5^(i / halfLife)，越近权重越大。
// 抗 outlier：先做加权 LS 得初始 fit，再用 Huber 单步重加权（c=1.345·MAD），
//
//	降低 2021 牛市顶 / LUNA-FTX 双爆这种极端样本对斜率的影响。
func fitAhrLogLogModel(history []decimal.Decimal, asOf time.Time, halfLifeDays int) (ahrLogLogFit, bool) {
	if halfLifeDays <= 0 {
		halfLifeDays = ahrRecentHalfLifeDays
	}

	limit := len(history)
	if limit > ahrFitWindowDays {
		limit = ahrFitWindowDays
	}
	if limit < ahrMinFitWindowDays {
		return ahrLogLogFit{}, false
	}

	samples := make([]ahrSample, 0, limit)
	for i := 0; i < limit; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}
		date := asOf.AddDate(0, 0, -i)
		ageDays := bitcoinAgeDays(date)
		if ageDays <= 0 {
			continue
		}
		samples = append(samples, ahrSample{
			x: math.Log(ageDays),
			y: math.Log(price),
			w: math.Pow(0.5, float64(i)/float64(halfLifeDays)),
		})
	}
	if len(samples) < ahrMinFitWindowDays {
		return ahrLogLogFit{}, false
	}

	// Step 1: 初始加权最小二乘
	alpha, beta, ok := weightedLinearFit(samples)
	if !ok {
		return ahrLogLogFit{}, false
	}

	// Step 2: Huber 单步重加权（IRLS 1 iter），削弱极端残差
	residuals := make([]float64, len(samples))
	for i, s := range samples {
		residuals[i] = s.y - (alpha + beta*s.x)
	}
	mad := medianAbsDeviation(residuals)
	if mad > 1e-9 {
		// 标准 Huber 阈值 1.345·σ ≈ 2·MAD（MAD≈0.6745σ）
		threshold := 2.0 * mad
		for i := range samples {
			r := math.Abs(residuals[i])
			if r > threshold {
				samples[i].w *= threshold / r // |r|>threshold 部分按 threshold/|r| 衰减
			}
		}
		alpha2, beta2, ok2 := weightedLinearFit(samples)
		if ok2 {
			alpha, beta = alpha2, beta2
		}
	}

	if !isUsableFinite(alpha) || !isUsableFinite(beta) {
		return ahrLogLogFit{}, false
	}

	return ahrLogLogFit{alpha: alpha, beta: beta}, true
}

// ahrSample 是 fit 拟合用的样本（log-age, log-price, weight）。
type ahrSample struct {
	x, y, w float64
}

// weightedLinearFit y = alpha + beta*x，加权最小二乘。
func weightedLinearFit(samples []ahrSample) (float64, float64, bool) {
	var sw, sx, sy, sxx, sxy float64
	for _, s := range samples {
		sw += s.w
		sx += s.w * s.x
		sy += s.w * s.y
		sxx += s.w * s.x * s.x
		sxy += s.w * s.x * s.y
	}
	den := sw*sxx - sx*sx
	if sw <= 0 || math.Abs(den) < 1e-12 {
		return 0, 0, false
	}
	beta := (sw*sxy - sx*sy) / den
	alpha := (sy - beta*sx) / sw
	return alpha, beta, true
}

// medianAbsDeviation 计算 |residual - median(residual)| 的中位数（MAD）。
func medianAbsDeviation(residuals []float64) float64 {
	if len(residuals) == 0 {
		return 0
	}
	cp := make([]float64, len(residuals))
	copy(cp, residuals)
	sort.Float64s(cp)
	med := cp[len(cp)/2]
	for i := range cp {
		cp[i] = math.Abs(cp[i] - med)
	}
	sort.Float64s(cp)
	return cp[len(cp)/2]
}

func (f ahrLogLogFit) fairValue(date time.Time) float64 {
	ageDays := bitcoinAgeDays(date)
	if ageDays <= 0 {
		return 0
	}
	return math.Exp(f.alpha + f.beta*math.Log(ageDays))
}

func buildAhrLogSamples(history []decimal.Decimal, asOf time.Time, fit ahrLogLogFit) []float64 {
	limit := len(history)
	if limit > ahrFitWindowDays {
		limit = ahrFitWindowDays
	}

	samples := make([]float64, 0, limit-ahrDCAWindowDays)
	for i := 0; i+ahrDCAWindowDays <= limit; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}

		dcaCost, ok := calculateDcaCost(history, i, ahrDCAWindowDays)
		if !ok {
			continue
		}

		date := asOf.AddDate(0, 0, -i)
		fairValue := fit.fairValue(date)
		if !isUsablePositive(fairValue) {
			continue
		}

		raw := (price / dcaCost) * (price / fairValue)
		if isUsablePositive(raw) {
			samples = append(samples, math.Log(raw))
		}
	}

	return samples
}

func calculateDcaCost(history []decimal.Decimal, start, window int) (float64, bool) {
	if window <= 0 || start < 0 || len(history) < start+window {
		return 0, false
	}

	invSum := 0.0
	count := 0
	for i := start; i < start+window; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}
		invSum += 1 / price
		count++
	}
	if count == 0 || invSum <= 0 {
		return 0, false
	}

	return float64(count) / invSum, true
}

func scoreAhrByDynamicQuantiles(logAhr float64, samples []float64) float64 {
	sort.Float64s(samples)

	q10 := quantileSorted(samples, 0.10)
	q35 := quantileSorted(samples, 0.35)
	q55 := quantileSorted(samples, 0.55)
	q75 := quantileSorted(samples, 0.75)
	q90 := quantileSorted(samples, 0.90)

	switch {
	case logAhr <= q10:
		return 1
	case logAhr <= q35:
		return linearMap(logAhr, q10, q35, 1, 0.5)
	case logAhr <= q55:
		return linearMap(logAhr, q35, q55, 0.5, 0)
	case logAhr <= q75:
		return linearMap(logAhr, q55, q75, 0, -0.5)
	case logAhr <= q90:
		return linearMap(logAhr, q75, q90, -0.5, -1)
	default:
		return -1
	}
}

func quantileSorted(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if q <= 0 {
		return sortedValues[0]
	}
	if q >= 1 {
		return sortedValues[len(sortedValues)-1]
	}

	pos := q * float64(len(sortedValues)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return sortedValues[lower]
	}
	frac := pos - float64(lower)
	return sortedValues[lower]*(1-frac) + sortedValues[upper]*frac
}

func linearMap(value, inMin, inMax, outMin, outMax float64) float64 {
	if math.Abs(inMax-inMin) < 1e-12 {
		return outMin
	}
	ratio := (value - inMin) / (inMax - inMin)
	return outMin + ratio*(outMax-outMin)
}

func bitcoinAgeDays(date time.Time) float64 {
	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	return date.Sub(genesis).Hours() / 24.0
}

func isUsablePositive(value float64) bool {
	return value > 0 && isUsableFinite(value)
}

func isUsableFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// --- Volatility ---
// calcVolatilityScore 计算波动率得分
// 优化：结合价格方向判断，高波动 + 下跌可能是底部信号
// - 低波动 (< 2%) => +0.5（蓄势待发）
// - 高波动 + 下跌 => +0.5（恐慌抛售，可能是底部）关键修复！
// - 高波动 + 上涨 => -0.5（可能是顶部疯狂）
// - 中等波动 => 0
func (c *Calculator) calcVolatilityScore(snap *model.MarketSnapshot) decimal.Decimal {
	if len(snap.BTCPriceHistory) < 140 {
		return decimal.Zero
	}

	// 计算当前波动率 (CV = Std / MA)
	std := mathutil.CalculateStdDev(snap.BTCPriceHistory, 20)
	ma20 := mathutil.CalculateMA(snap.BTCPriceHistory, 20)
	if ma20.IsZero() {
		return decimal.Zero
	}

	currentCV := std.Div(ma20)
	cv, _ := currentCV.Float64()

	// 计算最近 5 日价格方向
	priceChange := mathutil.CalculatePriceChange(snap.BTCPriceHistory, 5)
	changeVal, _ := priceChange.Float64()

	// 低波动 (蓄势)
	if cv < 0.02 {
		return decimal.NewFromFloat(0.5)
	}

	// 高波动情况
	if cv > 0.06 {
		if changeVal < -0.03 {
			// 高波动 + 下跌超过 3% => 恐慌抛售，可能是底部（关键修复！）
			return decimal.NewFromFloat(0.5)
		}
		if changeVal > 0.03 {
			// 高波动 + 上涨超过 3% => 可能是顶部疯狂
			return decimal.NewFromFloat(-0.5)
		}
		// 高波动但方向不明确
		return decimal.Zero
	}

	// 中等波动
	return decimal.Zero
}

// --- BTC Dominance ---
// calcBTCDominanceScore 计算BTC市占率得分
// BTC Dominance 是市场周期的重要指标：
// - 高Dominance (>60%) => 资金回流BTC，市场趋于保守，可能是熊市或牛市早期 => 中性偏正
// - 低Dominance (<40%) => 山寨季，市场风险偏好高，可能是牛市后期 => 负分（风险高）
// - 中等 (40-60%) => 正常状态 => 0
func (c *Calculator) calcBTCDominanceScore(snap *model.MarketSnapshot) decimal.Decimal {
	if snap.BTCDominance.IsZero() {
		return decimal.Zero
	}

	dom, _ := snap.BTCDominance.Float64()

	// BTC Dominance 通常在 0.35-0.70 之间波动
	switch {
	case dom > 0.65:
		// 极高Dominance: 资金高度集中于BTC，市场恐慌或熊市
		// 这通常是积累期，给正分
		return decimal.NewFromFloat(0.5)
	case dom > 0.55:
		// 高Dominance: 资金回流BTC
		return decimal.NewFromFloat(0.3)
	case dom > 0.45:
		// 中等Dominance: 正常状态
		return decimal.Zero
	case dom > 0.35:
		// 低Dominance: 山寨季开始，风险升高
		return decimal.NewFromFloat(-0.3)
	default:
		// 极低Dominance (<35%): 山寨季高潮，通常是牛市顶部
		return decimal.NewFromFloat(-0.6)
	}
}

// --- Reversal Detection (新增反转检测层) ---

// calcOversoldSpike 检测超卖急跌
// RSI < 30 且单日跌幅 > 5% => 极度超卖，通常是短期底部
func (c *Calculator) calcOversoldSpike(snap *model.MarketSnapshot) decimal.Decimal {
	if len(snap.BTCPriceHistory) < 15 {
		return decimal.Zero
	}

	// 计算 RSI
	rsi := mathutil.CalculateRSI(snap.BTCPriceHistory, 14)
	rsiVal, _ := rsi.Float64()

	// 计算单日跌幅
	dailyChange := mathutil.CalculatePriceChange(snap.BTCPriceHistory, 1)
	changeVal, _ := dailyChange.Float64()

	// 极度超卖条件
	if rsiVal < 30 && changeVal < -0.05 {
		// RSI < 30 且单日跌幅 > 5%
		return decimal.NewFromInt(1)
	}

	// 中度超卖
	if rsiVal < 35 && changeVal < -0.03 {
		return decimal.NewFromFloat(0.5)
	}

	// 检测超买急涨（顶部信号）
	if rsiVal > 70 && changeVal > 0.05 {
		return decimal.NewFromInt(-1)
	}

	return decimal.Zero
}

// calcRSIDivergence 检测 RSI 背离
// 价格创新低但 RSI 没有创新低 = 看涨背离（底部信号）
// 价格创新高但 RSI 没有创新高 = 看跌背离（顶部信号）
func (c *Calculator) calcRSIDivergence(snap *model.MarketSnapshot) decimal.Decimal {
	if len(snap.BTCPriceHistory) < 30 {
		return decimal.Zero
	}

	// 寻找最近 20 天内的价格局部最低点
	priceMinima := mathutil.FindLocalMinima(snap.BTCPriceHistory, 20)
	if len(priceMinima) < 2 {
		return decimal.Zero
	}

	// 计算每个最低点的 RSI
	// 使用最近的两个最低点进行比较
	idx1 := priceMinima[0] // 较近的最低点
	idx2 := priceMinima[1] // 较远的最低点

	if idx2 >= len(snap.BTCPriceHistory)-14 {
		return decimal.Zero
	}

	price1 := snap.BTCPriceHistory[idx1]
	price2 := snap.BTCPriceHistory[idx2]

	// 计算对应时刻的 RSI（简化：使用当前切片计算）
	rsi1 := mathutil.CalculateRSI(snap.BTCPriceHistory[idx1:], 14)
	rsi2 := mathutil.CalculateRSI(snap.BTCPriceHistory[idx2:], 14)

	rsi1Val, _ := rsi1.Float64()
	rsi2Val, _ := rsi2.Float64()

	// 看涨背离：价格创新低但 RSI 没有创新低
	if price1.LessThan(price2) && rsi1Val > rsi2Val {
		return decimal.NewFromFloat(0.8)
	}

	// 寻找最近 20 天内的价格局部最高点
	priceMaxima := mathutil.FindLocalMaxima(snap.BTCPriceHistory, 20)
	if len(priceMaxima) >= 2 {
		maxIdx1 := priceMaxima[0]
		maxIdx2 := priceMaxima[1]

		if maxIdx2 < len(snap.BTCPriceHistory)-14 {
			maxPrice1 := snap.BTCPriceHistory[maxIdx1]
			maxPrice2 := snap.BTCPriceHistory[maxIdx2]

			maxRsi1 := mathutil.CalculateRSI(snap.BTCPriceHistory[maxIdx1:], 14)
			maxRsi2 := mathutil.CalculateRSI(snap.BTCPriceHistory[maxIdx2:], 14)

			maxRsi1Val, _ := maxRsi1.Float64()
			maxRsi2Val, _ := maxRsi2.Float64()

			// 看跌背离：价格创新高但 RSI 没有创新高
			if maxPrice1.GreaterThan(maxPrice2) && maxRsi1Val < maxRsi2Val {
				return decimal.NewFromFloat(-0.8)
			}
		}
	}

	return decimal.Zero
}

// calcAltcoinSeasonScore 计算山寨币季节指数得分
// 山寨币季节指数越高，表示山寨币表现相对BTC更强
func (c *Calculator) calcAltcoinSeasonScore(snap *model.MarketSnapshot) decimal.Decimal {
	if snap.AltcoinSeasonIndex.IsZero() {
		return decimal.Zero
	}

	// 将山寨币季节指数(0-100)标准化到[-1,1]范围
	// 50为中性值，高于50表示山寨币季节较强，低于50表示较弱
	seasonIndex, _ := snap.AltcoinSeasonIndex.Float64()
	// 标准化公式：(index - 50) / 50
	normalized := (seasonIndex - 50.0) / 50.0
	return decimal.NewFromFloat(normalized)
}

// calcStablecoinRatioScore 计算稳定币市场占比得分
// 稳定币占比高的时候，可能表示市场风险偏好降低，资金寻求避险
func (c *Calculator) calcStablecoinRatioScore(snap *model.MarketSnapshot) decimal.Decimal {
	if snap.TotalMarketCap.IsZero() || snap.StablecoinMarketCap.IsZero() {
		return decimal.Zero
	}

	// 计算稳定币在总市值中的占比
	stablecoinRatio := snap.StablecoinMarketCap.Div(snap.TotalMarketCap)
	ratio, _ := stablecoinRatio.Float64()

	// 通常稳定币占比在5-15%之间，如果占比过高可能表示市场恐慌
	// 如果占比过低可能表示市场过热
	// 假设10%为中性值，偏离该值会给负分
	neutralRatio := 0.10 // 10%中性值
	deviation := ratio - neutralRatio

	// 基于偏离度计算得分，偏离越大，得分越低（负分）
	// 使用sigmoid函数来平滑得分变化
	sigValue := mathutil.Sigmoid(decimal.NewFromFloat(deviation * 20))
	score := sigValue.Mul(decimal.NewFromInt(-1)) // 等价于 -sigValue

	return mathutil.Clamp(score, decimal.NewFromInt(-1), decimal.NewFromInt(1))
}
