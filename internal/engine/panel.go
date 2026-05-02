// CoinMan v2: IndicatorPanel —— 投资盘面（无评分，无 action）
//
// BuildPanel() 直接从 MarketSnapshot 计算所有指标，按 6 个 domain 分组返回。
// 每个指标包含：原始值、历史分位、解读标签、数据源、更新时间、备注。
//
// 设计原则：
//   - 指标都是数学严格的原始量（不做 sigmoid/scaling 隐式压缩）
//   - 历史分位（q）告诉 Claude 当前值在历史分布中的位置
//   - label 仅用于人类速览，不应作为决策依据
//   - 决策由 skill SKILL.md（知识库）+ Claude 综合完成

package engine

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Ricaardo/guanfu/internal/mathutil"
	"github.com/Ricaardo/guanfu/internal/model"

	"github.com/shopspring/decimal"
)

// halvingDates BTC 减半历史日期（UTC）
var halvingDates = []time.Time{
	time.Date(2012, 11, 28, 0, 0, 0, 0, time.UTC), // 50 → 25 BTC
	time.Date(2016, 7, 9, 0, 0, 0, 0, time.UTC),   // 25 → 12.5
	time.Date(2020, 5, 11, 0, 0, 0, 0, time.UTC),  // 12.5 → 6.25
	time.Date(2024, 4, 20, 0, 0, 0, 0, time.UTC),  // 6.25 → 3.125
	time.Date(2028, 4, 20, 0, 0, 0, 0, time.UTC),  // 估计：3.125 → 1.5625
}

// BuildPanel 构造 v2 指标盘面
func (c *Calculator) BuildPanel(snap *model.MarketSnapshot) *model.IndicatorPanel {
	dataDate := snap.Date.Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)

	panel := &model.IndicatorPanel{
		Date: dataDate,
		Snapshot: model.SnapshotData{
			BTCPrice:            f(snap.BTCPrice),
			ETHPrice:            f(snap.ETHPrice),
			BTCDominance:        f(snap.BTCDominance),
			TotalMarketCap:      f(snap.TotalMarketCap),
			StablecoinMarketCap: f(snap.StablecoinMarketCap),
			FearGreed:           f(snap.FearGreedIndex),
			DataDate:            dataDate,
		},
		Cycle:         map[string]model.Indicator{},
		Valuation:     map[string]model.Indicator{},
		Network:       map[string]model.Indicator{},
		Positioning:   map[string]model.Indicator{},
		Macro:         map[string]model.Indicator{},
		Flow:          map[string]model.Indicator{},
		StaleWarnings: append([]string(nil), snap.SourceWarnings...),
	}

	c.fillCycle(panel, snap, now)
	c.fillValuation(panel, snap, now)
	c.fillNetwork(panel, snap, now)
	c.fillPositioning(panel, snap, now)
	c.fillMacro(panel, snap, now)
	c.fillFlow(panel, snap, now)

	c.persistAndAnnotateHistory(panel, dataDate)
	panel.StaleWarnings = dedupeStrings(panel.StaleWarnings)

	return panel
}

// historyTracked 列出从 history.db 取分位的指标 → 所属 domain。
// 这些指标缺少长 BTC-kline 派生历史，必须靠每日采集。
var historyTracked = map[string]string{
	"etf_net_flow_7d_usd":       "flow",
	"etf_net_flow_30d_usd":      "flow",
	"etf_total_assets_usd":      "flow",
	"stablecoin_market_cap_usd": "flow",
	"stablecoin_supply_30d_pct": "flow",
	"mempool_mb":                "network",
	"hash_rate_ehs":             "network",
	"difficulty_change_pct":     "network",
	"funding_rate_pct":          "positioning",
	"oi_to_mc":                  "positioning",
	"fear_greed":                "positioning",
	"dxy_60d_trend_pct":         "macro",
	"real_yield_10y_pct":        "macro",
	"m2_yoy":                    "macro",
	"spx_correlation_30d":       "macro",
}

const (
	historyLookbackDays = 730 // 2 年
	historyMinSamples   = 30  // 至少 30 天才算有意义
)

// persistAndAnnotateHistory 把今天的 tracked 指标写入 history.db，
// 同时为已有足够样本的指标回填 Quantile。
//
// 没有 Store（c.History == nil）时跳过 — Calculator 仍输出指标盘，仅缺历史分位。
func (c *Calculator) persistAndAnnotateHistory(p *model.IndicatorPanel, date string) {
	if c.History == nil {
		return
	}

	// 1) 收集今日值并批量写
	todayKV := map[string]float64{}
	for k, dom := range historyTracked {
		ind, ok := getDomainIndicator(p, dom, k)
		if !ok || (ind.Value == 0 && ind.Label == "") {
			continue
		}
		todayKV[k] = ind.Value
	}
	if len(todayKV) > 0 {
		if err := c.History.RecordMany(date, todayKV); err != nil {
			// 写失败不致命：仍输出本次盘面
			return
		}
	}

	// 2) 回填 Quantile（仅当样本 >= 30）
	for k, dom := range historyTracked {
		ind, ok := getDomainIndicator(p, dom, k)
		if !ok {
			continue
		}
		val, present := todayKV[k]
		if !present {
			continue
		}
		q, n, err := c.History.QuantileAsOf(k, val, historyLookbackDays, date)
		if err != nil || n < historyMinSamples {
			continue
		}
		ind.Quantile = q
		ind.Note = fmt.Sprintf("%s（历史分位基于 %d 天采集）", ind.Note, n)
		setDomainIndicator(p, dom, k, ind)
	}
}

// stablecoinMarketCapNDaysAgo 从 history.db 查 N 天前的稳定币市值
func (c *Calculator) stablecoinMarketCapNDaysAgo(asOfDate string, daysAgo int) (float64, bool) {
	if c.History == nil {
		return 0, false
	}
	t, err := time.Parse("2006-01-02", asOfDate)
	if err != nil {
		return 0, false
	}
	pastDate := t.AddDate(0, 0, -daysAgo).Format("2006-01-02")
	return c.History.ValueAt(pastDate, "stablecoin_market_cap_usd")
}

func getDomainIndicator(p *model.IndicatorPanel, domain, key string) (model.Indicator, bool) {
	m := domainMap(p, domain)
	if m == nil {
		return model.Indicator{}, false
	}
	ind, ok := m[key]
	return ind, ok
}

func setDomainIndicator(p *model.IndicatorPanel, domain, key string, ind model.Indicator) {
	m := domainMap(p, domain)
	if m == nil {
		return
	}
	m[key] = ind
}

func domainMap(p *model.IndicatorPanel, domain string) map[string]model.Indicator {
	switch domain {
	case "cycle":
		return p.Cycle
	case "valuation":
		return p.Valuation
	case "network":
		return p.Network
	case "positioning":
		return p.Positioning
	case "macro":
		return p.Macro
	case "flow":
		return p.Flow
	}
	return nil
}

// ----------------- Cycle 周期 -----------------

func (c *Calculator) fillCycle(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	btcTS := sourceTimestamp(snap.BTCPriceAsOf, ts)

	// halving cycle days: 距离上次减半天数 + 距离下次减半天数
	prevHalving, nextHalving := nearestHalvings(snap.Date)
	if !prevHalving.IsZero() {
		days := int(snap.Date.Sub(prevHalving).Hours() / 24)
		p.Cycle["days_since_halving"] = model.Indicator{
			Value:     float64(days),
			Label:     halvingPhaseLabel(days),
			Source:    "static",
			UpdatedAt: ts,
			Note:      fmt.Sprintf("上次减半: %s", prevHalving.Format("2006-01-02")),
		}
	}
	if !nextHalving.IsZero() {
		days := int(nextHalving.Sub(snap.Date).Hours() / 24)
		p.Cycle["days_to_halving"] = model.Indicator{
			Value:     float64(days),
			Source:    "static",
			UpdatedAt: ts,
			Note:      fmt.Sprintf("下次减半（估计）: %s", nextHalving.Format("2006-01-02")),
		}
	}

	// 200-week SMA + 偏离度
	if maW, ok := wmaSMA(snap.BTCPriceHistory, 200); ok {
		price := f(snap.BTCPrice)
		dev := (price - maW) / maW
		p.Cycle["sma_200w"] = model.Indicator{
			Value:     maW,
			Source:    "binance",
			UpdatedAt: btcTS,
			Note:      "200 周（1400 日）SMA — 历史价格地板",
		}
		p.Cycle["sma_200w_dev"] = model.Indicator{
			Value:     dev,
			Quantile:  displayQuantile(quantileOfDeviation(snap.BTCPriceHistory, 200*7, dev)),
			Label:     devLabel(dev),
			Source:    "binance",
			UpdatedAt: btcTS,
			Note:      "(price - 200wSMA) / 200wSMA。<0 历史抄底区，>1 牛市末期",
		}
	}

	// Mayer Multiple = price / 200d SMA
	if ma200d, ok := smaFromHistory(snap.BTCPriceHistory, 200); ok {
		mayer := f(snap.BTCPrice) / ma200d
		p.Cycle["mayer_multiple"] = model.Indicator{
			Value:     mayer,
			Quantile:  displayQuantile(quantileOfMayer(snap.BTCPriceHistory, 200)),
			Label:     mayerLabel(mayer),
			Source:    "binance",
			UpdatedAt: btcTS,
			Note:      "price / 200d SMA。<1 偏低估，>2.4 历史顶部区",
		}
	}

	// Pi Cycle Top: 111d MA vs 2 × 350d MA（顶部信号 = 111dMA 上穿 2×350dMA）
	ma111, ok1 := smaFromHistory(snap.BTCPriceHistory, 111)
	ma350, ok2 := smaFromHistory(snap.BTCPriceHistory, 350)
	if ok1 && ok2 {
		ratio := ma111 / (2 * ma350)
		p.Cycle["pi_cycle_top_ratio"] = model.Indicator{
			Value:     ratio,
			Label:     piCycleLabel(ratio),
			Source:    "binance",
			UpdatedAt: btcTS,
			Note:      "111dMA / (2×350dMA)。>=1 触发 Pi Cycle Top（历史顶部信号）",
		}
	}

	// Cycle phase 分类（基于 sma_200w_dev + days_since_halving 简单启发式）
	p.Cycle["phase"] = model.Indicator{
		Label:     classifyCyclePhase(snap, prevHalving),
		Source:    "derived",
		UpdatedAt: ts,
		Note:      "启发式分类: accumulation/markup/distribution/markdown",
	}
}

// ----------------- Valuation 估值 -----------------

func (c *Calculator) fillValuation(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	btcTS := sourceTimestamp(snap.BTCPriceAsOf, ts)
	onchainTS := sourceTimestamp(snap.OnchainValuationAsOf, ts)

	// AHR999 — raw 值与 q 来自同一套自适应 AHR 样本分布
	_, ahrRaw, ahrQ, ok := c.calcAhr999Detailed(snap)
	if ok && !ahrRaw.IsZero() {
		raw := f(ahrRaw)
		p.Valuation["ahr999"] = model.Indicator{
			Value:     raw,
			Quantile:  displayQuantile(ahrQ),
			Label:     ahrLabel(raw),
			Source:    "binance + adaptive log-log fit",
			UpdatedAt: btcTS,
			Note:      "九神 AHR999（已修正：调和均值 DCA + Huber 抗 outlier + 动态拟合）",
		}
	}

	if snap.OnchainValuationFetched {
		mvrv := f(snap.CapMVRVCur)
		nupl := f(snap.NUPL)
		z := f(snap.MVRVZScore)

		p.Valuation["mvrv"] = model.Indicator{
			Value:     mvrv,
			Quantile:  displayQuantile(f(snap.MVRVQuantile)),
			Label:     mvrvLabel(mvrv),
			Source:    "coinmetrics:CapMVRVCur",
			UpdatedAt: onchainTS,
			Note:      "MVRV = market cap / realized cap。CoinMetrics community metric",
		}
		p.Valuation["mvrv_z_score"] = model.Indicator{
			Value:     z,
			Label:     mvrvZLabel(z),
			Source:    "coinmetrics:CapMrktCurUSD+CapMVRVCur",
			UpdatedAt: onchainTS,
			Note:      "MVRV Z = (market cap - realized cap) / std(market cap)。realized cap direct or implied by CapMVRVCur",
		}
		p.Valuation["nupl"] = model.Indicator{
			Value:     nupl,
			Quantile:  displayQuantile(f(snap.NUPLQuantile)),
			Label:     nuplLabel(nupl),
			Source:    "coinmetrics:CapMVRVCur",
			UpdatedAt: onchainTS,
			Note:      "NUPL = (market cap - realized cap) / market cap = 1 - 1/MVRV",
		}
	} else {
		addStaleWarning(p, "coinmetrics valuation unavailable: MVRV/NUPL/MVRV Z shown as placeholders")
		p.Valuation["mvrv_z_score"] = model.Indicator{
			Source:    "coinmetrics (待接入)",
			UpdatedAt: ts,
			Note:      "需要 CoinMetrics CapMVRVCur + CapMrktCurUSD；若有 COINMETRICS_API_KEY 可尝试直接 CapRealUSD",
		}
		p.Valuation["nupl"] = model.Indicator{
			Source:    "coinmetrics (待接入)",
			UpdatedAt: ts,
			Note:      "NUPL = (market cap - realized cap) / market cap；无数据时不估算",
		}
	}
}

// ----------------- Network 网络 -----------------

func (c *Calculator) fillNetwork(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	networkTS := sourceTimestamp(snap.MempoolAsOf, ts)

	// 哈希率（EH/s）
	if !snap.HashRateEHs.IsZero() {
		hr := f(snap.HashRateEHs)
		p.Network["hash_rate_ehs"] = model.Indicator{
			Value:     hr,
			Label:     hashRateLabel(hr),
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "BTC 网络哈希率 (EH/s)。长期趋势上行 = 矿工对网络投票",
		}
	}

	// Hash ribbons（30d vs 60d）
	if snap.HashRibbonsLabel != "" {
		p.Network["hash_ribbons"] = model.Indicator{
			Label:     snap.HashRibbonsLabel,
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "30d MA vs 60d MA 交叉。'下行' = 矿工投降信号（历史抄底前兆）",
		}
	}

	// 难度调整
	if !snap.DifficultyChangePct.IsZero() {
		dc := f(snap.DifficultyChangePct)
		p.Network["difficulty_change_pct"] = model.Indicator{
			Value:     dc,
			Label:     difficultyLabel(dc),
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "上次难度调整 %。+ = 算力流入，- = 算力流出/投降",
		}
	}

	// Mempool 拥堵
	if !snap.MempoolMB.IsZero() {
		mb := f(snap.MempoolMB)
		p.Network["mempool_mb"] = model.Indicator{
			Value:     mb,
			Label:     mempoolLabel(mb),
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "Mempool 待打包字节数 (MB)。>100 拥堵 = 链上活跃 / 牛市顶部常见",
		}
	}
}

// ----------------- Positioning 杠杆 & 情绪 -----------------

func (c *Calculator) fillPositioning(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	fearGreedTS := sourceTimestamp(snap.FearGreedAsOf, ts)

	// Funding rate
	if !snap.BTCFundingRate.IsZero() {
		fr := f(snap.BTCFundingRate)
		p.Positioning["funding_rate_pct"] = model.Indicator{
			Value:     fr * 100, // 转百分比方便看
			Label:     fundingRateLabel(fr),
			Source:    "binance",
			UpdatedAt: ts,
			Note:      "BTC 永续合约资金费率 (%/8h)。>0.05% 高，<0 多头不愿付费 = 偏熊或反转",
		}
	}

	// OI / Market Cap
	// snap.BTCOpenInterest 存的是 BTC 数量（real.go fetchFuturesData 不再
	// 在那里乘价格，避免与 fetchBTCData 竞争）。这里折算 USD 后除以市值。
	if !snap.BTCOpenInterest.IsZero() && !snap.BTCPrice.IsZero() {
		oiUSD := f(snap.BTCOpenInterest) * f(snap.BTCPrice)
		mcBTC := f(snap.BTCPrice) * 19_700_000 // 大约流通量
		ratio := oiUSD / mcBTC
		p.Positioning["oi_to_mc"] = model.Indicator{
			Value:     ratio,
			Label:     oiLabel(ratio),
			Source:    "binance",
			UpdatedAt: ts,
			Note:      "OI(USD) / BTC 市值。>0.04 杠杆拥挤（清算风险），<0.015 杠杆松弛",
		}
	}

	// Fear & Greed
	if !snap.FearGreedIndex.IsZero() {
		fg := f(snap.FearGreedIndex)
		p.Positioning["fear_greed"] = model.Indicator{
			Value:     fg,
			Label:     fearGreedLabel(fg),
			Source:    "alternative.me",
			UpdatedAt: fearGreedTS,
			Note:      "0=极度恐慌, 100=极度贪婪。<20 历史抄底, >80 谨慎",
		}
	}

	// Altcoin Season Index（自算 — 与 blockchaincenter.net 定义一致）
	if !snap.AltcoinSeasonIndex.IsZero() {
		asi := f(snap.AltcoinSeasonIndex)
		p.Positioning["altcoin_season"] = model.Indicator{
			Value:     asi,
			Label:     altcoinSeasonLabel(asi),
			Source:    "computed:Binance Top50 vs BTC 90d",
			UpdatedAt: ts,
			Note:      "Top 50 中 90 日跑赢 BTC 的占比 (0-100)。>75=山寨季, <25=BTC 季。基于实时 Binance kline 计算",
		}
	}
}

// ----------------- Macro 宏观 -----------------

func (c *Calculator) fillMacro(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	if !snap.MacroFetched {
		// FRED_API_KEY 缺失或拉取失败 — 保留 placeholder
		p.Macro["dxy_60d_trend_pct"] = model.Indicator{
			Source:    "fred (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY 环境变量。DTWEXBGS 60 日趋势",
		}
		p.Macro["real_yield_10y_pct"] = model.Indicator{
			Source:    "fred (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY。DFII10 10Y TIPS",
		}
		p.Macro["m2_yoy"] = model.Indicator{
			Source:    "fred (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY。M2SL 同比",
		}
		p.Macro["spx_correlation_30d"] = model.Indicator{
			Source:    "computed (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY 拉 SP500 后计算",
		}
		return
	}

	// DXY 60d 趋势 (DTWEXBGS)
	if !snap.DXY60dTrendPct.IsZero() || !snap.DXYLatest.IsZero() {
		dxyTrend := f(snap.DXY60dTrendPct)
		p.Macro["dxy_60d_trend_pct"] = model.Indicator{
			Value:     dxyTrend,
			Label:     dxyTrendLabel(dxyTrend),
			Source:    "fred:DTWEXBGS",
			UpdatedAt: sourceTimestamp(snap.DXYAsOf, ts),
			Note:      fmt.Sprintf("Trade-Weighted USD (Broad) 60 日变化 %%。最新 %.2f (as of %s)。下行利好 BTC", f(snap.DXYLatest), snap.DXYAsOf),
		}
	}

	// 10Y 实际利率 (DFII10)
	if !snap.RealYield10YPct.IsZero() {
		ry := f(snap.RealYield10YPct)
		p.Macro["real_yield_10y_pct"] = model.Indicator{
			Value:     ry,
			Label:     realYieldLabel(ry),
			Source:    "fred:DFII10",
			UpdatedAt: sourceTimestamp(snap.RealYield10YAsOf, ts),
			Note:      fmt.Sprintf("10Y TIPS 实际利率 %%（as of %s）。>2%% 历史性逆风，<0%% 极度宽松", snap.RealYield10YAsOf),
		}
	}

	// M2 同比 (M2SL)
	if !snap.M2YoYPct.IsZero() || !snap.M2LatestB.IsZero() {
		m2yoy := f(snap.M2YoYPct)
		p.Macro["m2_yoy"] = model.Indicator{
			Value:     m2yoy,
			Label:     m2YoYLabel(m2yoy),
			Source:    "fred:M2SL",
			UpdatedAt: sourceTimestamp(snap.M2AsOf, ts),
			Note:      fmt.Sprintf("M2 货币供应同比 %%。最新 $%.2fT（as of %s）。扩张 = BTC 顺风", f(snap.M2LatestB)/1000, snap.M2AsOf),
		}
	}

	// BTC vs SPX 30d 相关
	if !snap.SPXCorrelation30d.IsZero() {
		corr := f(snap.SPXCorrelation30d)
		p.Macro["spx_correlation_30d"] = model.Indicator{
			Value:     corr,
			Label:     spxCorrLabel(corr),
			Source:    "fred:SP500 + binance:BTC",
			UpdatedAt: sourceTimestamp(snap.SPXAsOf, ts),
			Note:      fmt.Sprintf("BTC 与 SPX 30 日对数收益率 Pearson 相关（as of %s）。>0.5 BTC 强 risk-on，<0 BTC 走独立行情", snap.SPXAsOf),
		}
	}
}

func dxyTrendLabel(pct float64) string {
	switch {
	case pct < -3:
		return "美元大幅走弱（BTC 顺风）"
	case pct < -1:
		return "美元走弱"
	case pct < 1:
		return "美元横盘"
	case pct < 3:
		return "美元走强"
	default:
		return "美元大幅走强（BTC 逆风）"
	}
}

func realYieldLabel(pct float64) string {
	switch {
	case pct < 0:
		return "负实际利率（极度宽松，风险资产顺风）"
	case pct < 1:
		return "低位（BTC 顺风）"
	case pct < 2:
		return "正常"
	case pct < 2.5:
		return "高位（BTC 逆风）"
	default:
		return "极高（历史性逆风）"
	}
}

func m2YoYLabel(yoy float64) string {
	switch {
	case yoy < -1:
		return "收缩（罕见，2008/2022 后期出现）"
	case yoy < 2:
		return "停滞（流动性紧）"
	case yoy < 5:
		return "温和扩张"
	case yoy < 8:
		return "扩张（BTC 顺风）"
	default:
		return "强劲扩张（2020-21 印钞峰值）"
	}
}

func spxCorrLabel(c float64) string {
	switch {
	case c < -0.3:
		return "负相关（BTC 走独立避险行情）"
	case c < 0.2:
		return "弱相关（独立性较强）"
	case c < 0.5:
		return "中等相关"
	case c < 0.7:
		return "强相关（高 beta 风险资产）"
	default:
		return "极强相关（与股市同步度高）"
	}
}

// ----------------- Flow 资金流 -----------------

func (c *Calculator) fillFlow(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	etfTS := sourceTimestamp(snap.ETFAsOf, ts)

	// 现货 BTC ETF 净流入
	if !snap.ETFNetFlow7dUSD.IsZero() || !snap.ETFNetFlow30dUSD.IsZero() {
		f7 := f(snap.ETFNetFlow7dUSD)
		f30 := f(snap.ETFNetFlow30dUSD)
		assets := f(snap.ETFTotalAssetUSD)

		note7 := "现货 BTC ETF 7 日净流入 (USD)。2024+ 主要边际驱动"
		if snap.ETFStaleDays >= 2 {
			note7 += fmt.Sprintf("（数据距今 %d 天）", snap.ETFStaleDays)
		}

		p.Flow["etf_net_flow_7d_usd"] = model.Indicator{
			Value:     f7,
			Label:     etfFlowLabel(f7, 7),
			Source:    "sosovalue",
			UpdatedAt: etfTS,
			Note:      note7,
		}
		p.Flow["etf_net_flow_30d_usd"] = model.Indicator{
			Value:     f30,
			Label:     etfFlowLabel(f30, 30),
			Source:    "sosovalue",
			UpdatedAt: etfTS,
			Note:      "30 日累计净流入。持续正流入 = 机构稳定接盘",
		}
		if assets > 0 {
			p.Flow["etf_total_assets_usd"] = model.Indicator{
				Value:     assets,
				Source:    "sosovalue",
				UpdatedAt: etfTS,
				Note:      "ETF 总持仓 USD。整体规模代表机构覆盖深度",
			}
		}
	}

	// Stablecoin 总市值（入库供后续计算 30d 增速）
	if !snap.StablecoinMarketCap.IsZero() {
		scap := f(snap.StablecoinMarketCap)
		p.Flow["stablecoin_market_cap_usd"] = model.Indicator{
			Value:     scap,
			Source:    "coingecko",
			UpdatedAt: ts,
			Note:      "主要稳定币（USDT+USDC+DAI+FDUSD+FRAX）总市值。每日入库，攒够 30 天后回显 stablecoin_supply_30d_pct",
		}

		// 从 history.db 中查 30 天前的市值算出增速
		if c.History != nil {
			asOf := snap.Date.Format("2006-01-02")
			pastCap, pastOK := c.stablecoinMarketCapNDaysAgo(asOf, 30)
			if pastOK && pastCap > 0 {
				growth := (scap - pastCap) / pastCap * 100
				p.Flow["stablecoin_supply_30d_pct"] = model.Indicator{
					Value:     growth,
					Label:     stablecoinGrowthLabel(growth),
					Source:    "coingecko + history.db",
					UpdatedAt: ts,
					Note:      "稳定币总市值 30 日增速（基于 history.db 采集）。扩张 = 加密链上流动性进入",
				}
			}
		}
	}

	// ETH/BTC ratio（保留 — 资金风险偏好代理）
	if !snap.ETHPrice.IsZero() && !snap.BTCPrice.IsZero() {
		ratio := f(snap.ETHPrice) / f(snap.BTCPrice)
		p.Flow["eth_btc_ratio"] = model.Indicator{
			Value:     ratio,
			Quantile:  displayQuantile(ethBtcRatioQuantile(snap)),
			Label:     ethBtcLabel(ratio),
			Source:    "binance",
			UpdatedAt: sourceTimestamp(snap.ETHPriceAsOf, sourceTimestamp(snap.BTCPriceAsOf, ts)),
			Note:      "ETH/BTC 比率。低位 = 资金避险偏 BTC，高位 = 风险偏好 ETH/Alt",
		}
	}
}

// ============== Helpers ==============

func f(d decimal.Decimal) float64 { v, _ := d.Float64(); return v }

func sourceTimestamp(asOf, fallback string) string {
	if asOf == "" {
		return fallback
	}
	if t, err := time.Parse("2006-01-02", asOf); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339, asOf); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339Nano, asOf); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return asOf
}

func addStaleWarning(p *model.IndicatorPanel, warning string) {
	if warning == "" {
		return
	}
	p.StaleWarnings = append(p.StaleWarnings, warning)
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

// displayQuantile keeps the JSON contract strict: q is shown only when the
// value is a real historical percentile in [0,1]. Unknown quantiles use 0 so
// the existing omitempty tag suppresses q in JSON output.
func displayQuantile(q float64) float64 {
	if q < 0 || q > 1 || math.IsNaN(q) || math.IsInf(q, 0) {
		return 0
	}
	return q
}

// nearestHalvings 返回当前日期的前后减半事件
func nearestHalvings(now time.Time) (prev, next time.Time) {
	for i, h := range halvingDates {
		if h.After(now) {
			next = h
			if i > 0 {
				prev = halvingDates[i-1]
			}
			return
		}
	}
	if len(halvingDates) > 0 {
		prev = halvingDates[len(halvingDates)-1]
	}
	return
}

// halvingPhaseLabel 基于减半后天数的标签
func halvingPhaseLabel(daysSince int) string {
	switch {
	case daysSince < 180:
		return "halving 后早期 (0-6m)"
	case daysSince < 540:
		return "halving 后牛市期 (6-18m)"
	case daysSince < 900:
		return "顶部 / 分配期 (18-30m)"
	case daysSince < 1260:
		return "熊市 / 积累期 (30-42m)"
	default:
		return "halving 前期"
	}
}

// classifyCyclePhase 周期阶段启发式分类
func classifyCyclePhase(snap *model.MarketSnapshot, prevHalving time.Time) string {
	if prevHalving.IsZero() {
		return "unknown"
	}
	daysSince := int(snap.Date.Sub(prevHalving).Hours() / 24)

	// 200wSMA 偏离度作为辅助
	devLabel := ""
	if maW, ok := wmaSMA(snap.BTCPriceHistory, 200); ok && maW > 0 {
		dev := (f(snap.BTCPrice) - maW) / maW
		if dev > 1.5 {
			devLabel = "极高"
		} else if dev > 0.5 {
			devLabel = "高"
		} else if dev < -0.2 {
			devLabel = "极低"
		} else if dev < 0.2 {
			devLabel = "低"
		}
	}

	switch {
	case daysSince < 180 && (devLabel == "低" || devLabel == "极低"):
		return "early_post_halving"
	case daysSince < 540:
		return "markup"
	case daysSince < 900 && (devLabel == "高" || devLabel == "极高"):
		return "distribution_risk"
	case daysSince >= 540 && daysSince < 900:
		return "late_markup_or_top"
	case daysSince >= 900 && (devLabel == "低" || devLabel == "极低"):
		return "accumulation"
	default:
		return "transition"
	}
}

// smaFromHistory 简单移动平均（取最近 N 个）
func smaFromHistory(history []decimal.Decimal, n int) (float64, bool) {
	if len(history) < n {
		return 0, false
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += f(history[i])
	}
	return sum / float64(n), true
}

// wmaSMA 周线 SMA = 取 7N 日的算术均（近似 N 周 SMA）
func wmaSMA(history []decimal.Decimal, weeks int) (float64, bool) {
	return smaFromHistory(history, weeks*7)
}

// quantileOfDeviation 当前偏离值在过去 windowDays 偏离分布中的分位
func quantileOfDeviation(history []decimal.Decimal, windowDays int, currentDev float64) float64 {
	if len(history) < windowDays+200 {
		return -1
	}
	devs := make([]float64, 0, windowDays)
	for i := 0; i < windowDays && i+200 < len(history); i++ {
		// 滚动计算每天的 200dMA 偏离
		ma, ok := smaFromHistory(history[i:], 200)
		if !ok {
			continue
		}
		price := f(history[i])
		if ma > 0 {
			devs = append(devs, (price-ma)/ma)
		}
	}
	return quantileRank(devs, currentDev)
}

// quantileOfMayer 当前 Mayer 在过去分布中的分位
func quantileOfMayer(history []decimal.Decimal, ma int) float64 {
	if len(history) < ma+1000 {
		return -1
	}
	mayers := make([]float64, 0, 1000)
	for i := 0; i < 1000 && i+ma < len(history); i++ {
		m, ok := smaFromHistory(history[i:], ma)
		if !ok || m <= 0 {
			continue
		}
		mayers = append(mayers, f(history[i])/m)
	}
	current, ok := smaFromHistory(history, ma)
	if !ok || current <= 0 {
		return -1
	}
	return quantileRank(mayers, f(history[0])/current)
}

// ethBtcRatioQuantile
func ethBtcRatioQuantile(snap *model.MarketSnapshot) float64 {
	if len(snap.ETHPriceHistory) < 365 || len(snap.BTCPriceHistory) < 365 {
		return -1
	}
	ratios := make([]float64, 0, 365)
	for i := 0; i < 365; i++ {
		btc := f(snap.BTCPriceHistory[i])
		eth := f(snap.ETHPriceHistory[i])
		if btc > 0 {
			ratios = append(ratios, eth/btc)
		}
	}
	current := f(snap.ETHPrice) / f(snap.BTCPrice)
	return quantileRank(ratios, current)
}

// quantileRank 在 sorted-after-input distribution 里 v 的分位 [0, 1]
func quantileRank(samples []float64, v float64) float64 {
	if len(samples) == 0 || !isUsableFinite(v) {
		return -1
	}
	sorted := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if isUsableFinite(sample) {
			sorted = append(sorted, sample)
		}
	}
	if len(sorted) == 0 {
		return -1
	}
	sort.Float64s(sorted)
	// 二分定位：<= v 的样本占比，与 history.Store.Quantile 语义一致。
	belowOrEqual := sort.Search(len(sorted), func(i int) bool { return sorted[i] > v })
	return float64(belowOrEqual) / float64(len(sorted))
}

// ============== Labels ==============
//
// 以下 label 函数的阈值基于 2013-2024 周期经验，仅用于人类速览。
// 2024 ETF 通过后波动结构可能已变 — label 不可作为决策依据。
// Claude 决策时应以 q（历史分位）为准，label 只作辅助提示。

func devLabel(dev float64) string {
	switch {
	case dev < -0.3:
		return "深度低估（200wSMA 之下）"
	case dev < 0:
		return "低于 200wSMA"
	case dev < 0.5:
		return "正常区"
	case dev < 1.5:
		return "偏高"
	default:
		return "极端高估"
	}
}

func mayerLabel(m float64) string {
	switch {
	case m < 0.8:
		return "深度低估"
	case m < 1.0:
		return "偏低估"
	case m < 1.5:
		return "中性"
	case m < 2.4:
		return "偏高估"
	default:
		return "顶部区"
	}
}

func piCycleLabel(ratio float64) string {
	switch {
	case ratio >= 1.0:
		return "🔴 Pi Cycle Top 触发"
	case ratio > 0.85:
		return "接近触发"
	default:
		return "未触发"
	}
}

func ahrLabel(v float64) string {
	switch {
	case v < 0.45:
		return "极端抄底区（历史底部）"
	case v < 0.8:
		return "低估 / 定投区"
	case v < 1.2:
		return "合理"
	case v < 2.0:
		return "高估"
	default:
		return "泡沫"
	}
}

func mvrvLabel(v float64) string {
	switch {
	case v < 0.8:
		return "历史底部区"
	case v < 1.2:
		return "低估"
	case v < 2.0:
		return "中性"
	case v < 3.5:
		return "偏高"
	default:
		return "过热 / 顶部风险"
	}
}

func mvrvZLabel(v float64) string {
	switch {
	case v < 0:
		return "历史底部区"
	case v < 2:
		return "中性偏低"
	case v < 5:
		return "偏高"
	case v < 7:
		return "高位"
	default:
		return "顶部风险"
	}
}

func nuplLabel(v float64) string {
	switch {
	case v < 0:
		return "capitulation"
	case v < 0.25:
		return "hope / fear"
	case v < 0.5:
		return "optimism"
	case v < 0.75:
		return "belief"
	default:
		return "euphoria"
	}
}

func fundingRateLabel(fr float64) string {
	switch {
	case fr < 0:
		return "负值（多头不愿付，潜在反转）"
	case fr < 0.0001:
		return "极低"
	case fr < 0.0003:
		return "正常"
	case fr < 0.0005:
		return "偏高"
	default:
		return "过热（清算风险）"
	}
}

func oiLabel(r float64) string {
	switch {
	case r < 0.015:
		return "杠杆松弛"
	case r < 0.025:
		return "中性"
	case r < 0.035:
		return "偏拥挤"
	default:
		return "杠杆拥挤（清算风险）"
	}
}

func fearGreedLabel(fg float64) string {
	switch {
	case fg < 20:
		return "极度恐慌（历史抄底常见）"
	case fg < 40:
		return "恐慌"
	case fg < 60:
		return "中性"
	case fg < 80:
		return "贪婪"
	default:
		return "极度贪婪（顶部信号）"
	}
}

func stablecoinGrowthLabel(g float64) string {
	switch {
	case g < -3:
		return "收缩（流动性退潮）"
	case g < 1:
		return "停滞"
	case g < 5:
		return "温和扩张"
	default:
		return "强劲扩张（流动性宽松）"
	}
}

func etfFlowLabel(usd float64, days int) string {
	// 阈值按 7d 校准：>1B 强劲，>0 温和正，<-1B 大幅流出
	switch d := float64(days); {
	case usd < -1e9*d/7:
		return "大幅流出（机构减持）"
	case usd < -3e8*d/7:
		return "持续流出"
	case usd < 0:
		return "小幅流出"
	case usd < 5e8*d/7:
		return "微弱流入"
	case usd < 2e9*d/7:
		return "持续流入"
	default:
		return "强劲流入（机构 FOMO）"
	}
}

func hashRateLabel(eh float64) string {
	switch {
	case eh < 200:
		return "历史低位"
	case eh < 400:
		return "正常"
	case eh < 700:
		return "高位"
	default:
		return "历史峰值区"
	}
}

func difficultyLabel(d float64) string {
	switch {
	case d < -5:
		return "大幅下调（矿工投降信号 ⚠）"
	case d < 0:
		return "小幅下调"
	case d < 3:
		return "持平"
	case d < 8:
		return "上调（算力扩张）"
	default:
		return "大幅上调（FOMO 入场）"
	}
}

func mempoolLabel(mb float64) string {
	switch {
	case mb < 5:
		return "畅通"
	case mb < 30:
		return "正常"
	case mb < 100:
		return "拥堵"
	default:
		return "极度拥堵（链上活跃高峰）"
	}
}

func altcoinSeasonLabel(v float64) string {
	switch {
	case v >= 75:
		return "山寨季（资金溢出 BTC → Alt）"
	case v >= 50:
		return "偏山寨季"
	case v >= 25:
		return "偏 BTC 季"
	default:
		return "BTC 季（资金集中 BTC）"
	}
}

func ethBtcLabel(r float64) string {
	switch {
	case r < 0.030:
		return "ETH 极弱（避险偏 BTC）"
	case r < 0.045:
		return "ETH 弱"
	case r < 0.06:
		return "中性"
	case r < 0.075:
		return "ETH 偏强"
	default:
		return "ETH 极强（风险偏好高）"
	}
}

// 防止 mathutil 没用到的编译警告
var _ = mathutil.CalculateMA
