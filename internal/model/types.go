package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// CurrentMarketSnapshotSchemaVersion guards the on-disk market cache contract.
// Bump this when MarketSnapshot gains fields that materially change panel output.
const CurrentMarketSnapshotSchemaVersion = 2

// MarketSnapshot 包含了计算宏观评分所需的所有市场数据
type MarketSnapshot struct {
	Date                  time.Time
	SnapshotSchemaVersion int // on-disk cache schema version

	// BTC 数据
	BTCPrice         decimal.Decimal   // 当前价格
	BTCVolume24h     decimal.Decimal   // 24h 成交量
	BTCPriceHistory  []decimal.Decimal // 历史价格 (用于计算 MA)
	BTCVolumeHistory []decimal.Decimal // 历史成交量 (用于计算 MA)
	BTCPriceAsOf     string            // BTC 最新 K 线日期/时间

	// ETH 数据 (New)
	ETHPrice         decimal.Decimal
	ETHVolume24h     decimal.Decimal
	ETHPriceHistory  []decimal.Decimal
	ETHVolumeHistory []decimal.Decimal
	ETHPriceAsOf     string

	// 市场结构数据
	Top50Coins []CoinSnapshot // Top50 代币快照 (排除稳定币)

	// 资金数据
	TotalMarketCap        decimal.Decimal   // 总市值
	TotalMarketCapHistory []decimal.Decimal // 历史总市值 (用于计算变化率)

	// 稳定币流动性 (New)
	StablecoinMarketCap        decimal.Decimal
	StablecoinMarketCapHistory []decimal.Decimal

	USDTPriceCNY decimal.Decimal // USDT/CNY 价格
	USDPriceCNY  decimal.Decimal // USD/CNY 汇率

	// 衍生品与链上数据 (新增)
	BTCFundingRate  decimal.Decimal // 资金费率
	BTCOpenInterest decimal.Decimal // 合约持仓量 (USDT)

	// 网络数据 (mempool.space, v2 新增)
	HashRateEHs         decimal.Decimal // 当前哈希率 (EH/s)
	HashRibbonsLabel    string          // "上行(扩张)"/"下行(投降)"/"交叉中"
	DifficultyChangePct decimal.Decimal // 最近一次难度调整 %
	MempoolMB           decimal.Decimal // mempool 拥堵 (MB)
	MempoolAsOf         string

	// ETF 数据 (SoSoValue, v2 新增)
	ETFNetFlow7dUSD  decimal.Decimal // 7 日累计净流入
	ETFNetFlow30dUSD decimal.Decimal // 30 日累计净流入
	ETFTotalAssetUSD decimal.Decimal // 总持仓
	ETFStaleDays     int             // 数据距今天数
	ETFAsOf          string

	// CoinMetrics 链上估值（v2 新增）
	CapMVRVCur              decimal.Decimal // Market Cap / Realized Cap
	MVRVZScore              decimal.Decimal // (market cap - realized cap) / std(market cap)
	NUPL                    decimal.Decimal // (market cap - realized cap) / market cap
	MVRVQuantile            decimal.Decimal // MVRV 历史分位
	NUPLQuantile            decimal.Decimal // NUPL 历史分位
	MarketCapCurUSD         decimal.Decimal // CoinMetrics current market cap
	RealizedCapUSD          decimal.Decimal // CoinMetrics realized cap, direct or implied by MVRV
	OnchainValuationAsOf    string
	OnchainValuationFetched bool

	// FRED 宏观 (Phase 2 新增)
	DXY60dTrendPct    decimal.Decimal // DTWEXBGS 60 日变化 %
	DXYLatest         decimal.Decimal // DTWEXBGS 最新值
	DXYAsOf           string
	RealYield10YPct   decimal.Decimal // DFII10 最新 %
	RealYield10YAsOf  string
	M2YoYPct          decimal.Decimal // M2SL 同比 %
	M2LatestB         decimal.Decimal // M2SL 最新（十亿美元）
	M2AsOf            string
	SPXCorrelation30d decimal.Decimal // BTC vs SPX 30 日收益 Pearson
	SPXAsOf           string
	MacroFetched      bool // FRED 是否成功拉取

	// 情绪数据
	BTCDominance       decimal.Decimal // BTC 市占率 (0.00-1.00)
	FearGreedIndex     decimal.Decimal // 恐慌贪婪指数 (0-100)
	AltcoinSeasonIndex decimal.Decimal // 山寨季指数 (0-100)
	FearGreedAsOf      string

	// Cross-asset prices (v3 新增)
	GoldPriceUSD    decimal.Decimal // 黄金现货 USD/oz (Binance PAXG)
	QQQPrice        decimal.Decimal // QQQ ETF
	SPYPrice        decimal.Decimal // SPY ETF
	GoldPriceAsOf   string
	QQQPriceAsOf    string
	SPYPriceAsOf    string
	GoldHistory     []decimal.Decimal
	QQQHistory      []decimal.Decimal
	SPYHistory      []decimal.Decimal
	// Extended (v3.1, Futu)
	GoldETFPriceUSD decimal.Decimal // GLD 实物黄金 ETF
	GoldETFHistory  []decimal.Decimal
	GoldETFAsOf     string
	UUPPrice        decimal.Decimal // 做多美元 ETF (DXY proxy)
	UUPHistory      []decimal.Decimal
	UUPPriceAsOf    string
	VIXYPrice       decimal.Decimal // VIX 波动率 ETF
	VIXYHistory     []decimal.Decimal
	VIXYPriceAsOf   string
	CrossAssetFetched bool

	// 非致命数据源问题。BuildPanel 会透传到 stale_warnings。
	SourceWarnings []string
}

// CoinSnapshot 单个代币快照
type CoinSnapshot struct {
	Symbol       string
	Price        decimal.Decimal
	PriceHistory []decimal.Decimal // 历史价格 (至少需要 MA120 数据)
}

// === CoinMan v2: IndicatorPanel ===
//
// 投资盘面：纯指标，不做评分聚合，不输出 action。
// 由 Claude/skill 文档完成解读 + 决策。
//
// 6 个 domain 对应投资决策的不同视角：
//   - cycle: 在 4 年减半周期 / 长期均线意义上的位置
//   - valuation: 估值（MVRV / NUPL / AHR / Mayer / SOPR）
//   - network: 矿工 / 哈希率 / mempool 网络健康
//   - positioning: 杠杆 / 资金费率 / 情绪
//   - macro: 美元 / 实际利率 / 流动性 / 风险资产相关性
//   - flow: ETF 净流入 / 稳定币供应 / ETH/BTC 资金偏好

// Indicator 单个指标项
type Indicator struct {
	Value     float64 `json:"value"`                // 原始数值（无 sigmoid / scaling）
	Quantile  float64 `json:"q,omitempty"`          // 历史分位 [0,1]，越高表示当前值在历史上越高
	Label     string  `json:"label,omitempty"`      // 简短解读标签（"中性偏高", "极端低估"），仅参考
	Source    string  `json:"source,omitempty"`     // 数据源（"binance", "coinmetrics", "mempool.space", ...）
	UpdatedAt string  `json:"updated_at,omitempty"` // 数据更新时间（RFC3339）
	Note      string  `json:"note,omitempty"`       // 计算备注（如 "200d MA 历史不足，使用 100d"）
}

// SnapshotData 当前快照基础数据
type SnapshotData struct {
	BTCPrice            float64 `json:"btc_price"`
	ETHPrice            float64 `json:"eth_price,omitempty"`
	GoldPrice           float64 `json:"gold_price,omitempty"`
	QQQPrice            float64 `json:"qqq_price,omitempty"`
	SPYPrice            float64 `json:"spy_price,omitempty"`
	BTCDominance        float64 `json:"btc_dominance"`
	TotalMarketCap      float64 `json:"total_market_cap"`
	StablecoinMarketCap float64 `json:"stablecoin_market_cap"`
	FearGreed           float64 `json:"fear_greed"`
	DataDate            string  `json:"data_date"` // 主数据快照日期
}

// IndicatorPanel CoinMan v2 主输出
type IndicatorPanel struct {
	Date     string       `json:"date"`
	Snapshot SnapshotData `json:"snapshot"`

	// 8 个 domain（每个 domain 是 indicator name → Indicator 的 map）
	Cycle       map[string]Indicator `json:"cycle"`
	Valuation   map[string]Indicator `json:"valuation"`
	Network     map[string]Indicator `json:"network"`
	Positioning map[string]Indicator `json:"positioning"`
	Macro       map[string]Indicator `json:"macro"`
	Flow        map[string]Indicator `json:"flow"`
	Technical   map[string]Indicator `json:"technical"`
	CrossAsset  map[string]Indicator `json:"cross_asset"`

	// 元数据
	StaleWarnings []string `json:"stale_warnings,omitempty"` // 数据过时/缺失警告
}

// ScoreResult v1 评分结果（DEPRECATED）
//
// CoinMan v2 改用 IndicatorPanel 输出纯指标盘面，由 Claude/skill 文档完成解读。
// 此类型仅为 NewsEngine 的 Discord/Feishu 推送保留向后兼容；迁移完成后删除。
//
// 已废弃字段：TotalScore（设计性稀释 = 误导）、State（基于稀释总分）、
//
//	Action/Rationale/Conviction/Dispersion（v1.5 决策矩阵尝试，阈值拍脑袋）。
type ScoreResult struct {
	Date       string          `json:"date"`
	TotalScore decimal.Decimal `json:"score,omitempty"`       // deprecated, always 0
	State      string          `json:"state,omitempty"`       // deprecated, always "n/a"
	SignalDesc string          `json:"signal_desc,omitempty"` // deprecated

	// 子分仍保留（NewsEngine 推送会用），但不再做加权聚合
	TrendScore     decimal.Decimal `json:"trend_score"`
	ReversalScore  decimal.Decimal `json:"reversal_score"`
	ValuationScore decimal.Decimal `json:"valuation_score"`
	StructureScore decimal.Decimal `json:"structure_score"`

	Details map[string]decimal.Decimal `json:"details,omitempty"`
}

// Config 系统配置
type Config struct {
	Weights    Weights    `mapstructure:"weights"`
	Thresholds Thresholds `mapstructure:"thresholds"`
	API        APIConfig  `mapstructure:"api"`
}

type Weights struct {
	Trend     float64 `mapstructure:"trend"`     // 趋势层权重 (默认0.30)
	Reversal  float64 `mapstructure:"reversal"`  // 反转层权重 (默认0.25)
	Valuation float64 `mapstructure:"valuation"` // 估值层权重 (默认0.25)
	Structure float64 `mapstructure:"structure"` // 结构层权重 (默认0.20)
}

type Thresholds struct {
	BTCMAFast       int     `mapstructure:"btc_ma_fast"` // e.g. 120
	BTCMASlow       int     `mapstructure:"btc_ma_slow"` // e.g. 200
	TopCoinCount    int     `mapstructure:"top_coin_count"`
	DominanceHigh   float64 `mapstructure:"dominance_high"`
	DominanceLow    float64 `mapstructure:"dominance_low"`
	AHRHalfLifeDays int     `mapstructure:"ahr_half_life_days"` // 拟合权重半衰期，默认 365*4。短窗口对近期快牛快熊更敏感。
}

type APIConfig struct {
	Timeout string `mapstructure:"timeout"`
	Retries int    `mapstructure:"retries"`
	Mock    bool   `mapstructure:"mock"`
}
