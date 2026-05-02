---
name: btc-guanfu
description: |
  观复 / 观察万物之周期回归 — BTC 投资盘面 + 解读知识库。输出 8 个域（cycle / valuation / network / positioning / macro / flow / technical / cross_asset）的纯指标盘，每个指标含原始值、历史分位、解读标签、数据源。**不输出单一评分** — 而是基于多维指标一致性，输出概率加权的操作建议（买入/卖出/定投/等待），每条建议附完整证据链和反证。
  当用户问「BTC 该不该买/卖」、「比特币现在估值如何」、「加密底/顶在哪」、「定投区吗」、「AHR999/MVRV/哈希率/ETF 流入多少」、「BTC 周期位置」、「观复」、「BTC 技术指标」「BTC vs 黄金/美股/VIX」时触发。
  NOT for: 仅查 BTC 实时价格 → cmc-mcp；altcoin/memecoin → cmc-mcp / okx-dex；K 线图形态分析 → technical-analysis；链上钱包 → okx-wallet。
license: MIT
user-invocable: true
required_tools: []
---

# 观复 (btc-guanfu): BTC 投资盘面 + 解读手册

> 致虚极，守静笃。**万物并作，吾以观复。**
> ——《道德经》第十六章

## 核心哲学

**这个 skill 是观象台，不是观象者。** 老子言「万物并作，吾以观复」—— 万物纷纭起作，我守在静处看它们的往复回归。本工具做的就是这件事：

- **万物并作** ↔ 8 个 domain × 40+ 个指标（周期 / 估值 / 网络 / 杠杆 / 宏观 / 资金流 / 技术 / 跨资产）同时呈现
- **观复** ↔ 在历史分位中观察其往复 —— halving 周期回环、估值高低反覆、流入流出来去
- **致虚守静** ↔ 不输出单一数字评分，但基于多维一致性输出**概率加权建议**

具体分工：

- 二进制（`/Users/x/news/bin/btc-guanfu`）= 数据采集 + 指标计算 + 历史分位定位 = **盘**
- 本 SKILL.md = 每个指标的语义、历史阈值、组合 pattern、失效情形 + **决策框架** = **解读 + 建议**
- Claude 读完盘面 → 按 8 步读盘法 → 套用决策框架 → 输出概率加权操作建议

**v1 的错**（前身名 `coinman`）：用 1 个 0-100 总分 + 硬编码阈值输出 BUY/SELL。把多维结构压成 1 维丢失信息。**v2 更名「观复」**：不再产出单一评分，输出 8 域原始指标。**v3**：在指标盘基础上，由 Claude 依据 SKILL.md 决策框架输出概率加权建议——每条建议附完整证据链、反证和失效条件。

---

## 用法

```bash
# 完整盘面（人类可读）
/Users/x/news/bin/btc-guanfu

# JSON（喂 Claude / 程序）
/Users/x/news/bin/btc-guanfu --json | jq

# 仅看一个 domain
/Users/x/news/bin/btc-guanfu --domain cycle
/Users/x/news/bin/btc-guanfu --domain technical
/Users/x/news/bin/btc-guanfu --domain cross_asset
# ... valuation / network / positioning / macro / flow

# AHR999 拟合半衰期（默认 1460 = 4 年）
/Users/x/news/bin/btc-guanfu --halflife 730   # 2 年，对快牛快熊敏感
```

冷启动 ~5-8s（拉 Binance + mempool + SoSoValue + Yahoo/Yahoo + Futu），缓存命中 <1s。

**富途 OpenD 集成**：
- 默认自动连接 `127.0.0.1:11111`
- `FUTU_GATEWAY` 环境变量覆盖地址
- `FUTU_ENABLED=0` 禁用富途，走 Yahoo 降级
- 数据：QQQ / SPY / GLD / UUP (DXY proxy) / TLT / VIXY

**Futu Bridge 部署**（OpenD 默认需要加密握手，Go 端未实现）：
1. `pip install futu-api`（安装官方 Python SDK）
2. 将 `futu_bridge.py` 放到 `bin/` 同目录
3. guanfu 自动检测：Go 直连失败 → 调 Python bridge → Yahoo 降级
4. 如 bridge 脚本在其他位置，设 `FUTU_BRIDGE=/path/to/futu_bridge.py`

**重建二进制**：`cd ~/news && go build -o bin/btc-guanfu ./cmd/btc_guanfu`

---

## 历史分位 (history.db)

ETF / mempool / 资金费率 / 宏观这类指标没有公开历史 API，观复 通过 **SQLite 历史表** 自己每天采集一行，攒够样本后才能回填 `q` 字段。

**默认路径**：`~/.guanfu/history.db`（兼容老路径 `~/.coinman/history.db` 可手动 mv 过来），自动创建。可用 `--history-db /path/to/db` 指定，或 `GUANFU_NO_HISTORY=1` 禁用。

**采集的 14 个指标**：
- Flow: `etf_net_flow_7d_usd`, `etf_net_flow_30d_usd`, `etf_total_assets_usd`, `stablecoin_market_cap_usd`, `stablecoin_supply_30d_pct`
- Network: `mempool_mb`, `hash_rate_ehs`, `difficulty_change_pct`
- Positioning: `funding_rate_pct`, `oi_to_mc`, `fear_greed`
- Macro: `dxy_60d_trend_pct`, `real_yield_10y_pct`, `m2_yoy`, `spx_correlation_30d`

**何时显示 `q` 分位**：
- 累积 ≥ 30 天 → 开始显示，note 字段会注明"历史分位基于 N 天采集"
- 0-30 天 → 仅显示 value + label，不显示 q（数据不足）
- 回看窗口：730 天（2 年）

**首次部署后**：
- 第 1 天：14 个指标全部入库，但还没有 q 显示
- 第 30 天：开始有 q
- 第 365 天：q 完全有意义（覆盖全年节奏）
- 第 730 天：达到设计上限，老数据滚动淘汰

**直接查询历史**：
```bash
sqlite3 ~/.guanfu/history.db "SELECT date, value FROM daily_metrics WHERE key='etf_net_flow_30d_usd' ORDER BY date DESC LIMIT 30"
```

BTC 价格相关指标（`sma_200w_dev`, `mayer_multiple`, `eth_btc_ratio` 等）的 q 由 Binance kline 历史直接计算，不进 history.db。

---

## 盘面字段说明

每个指标返回：
- **`value`** 原始数值（无 sigmoid，无 scaling）
- **`q`** 历史分位 [0, 1]（当前值在历史分布的位置；q05 = 历史最低 5%，q95 = 历史最高 5%）
- **`label`** 简短解读标签（仅人类速览用，**Claude 不应直接信赖此 label 做决策**，应自己读 value + q）
- **`source`** 数据源
- **`updated_at`** 数据时间
- **`note`** 计算备注 / 数据时效性

---

## 指标手册（每个含定义、历史阈值、失效情形）

### 🌊 Cycle — 周期定位

#### `days_since_halving` / `days_to_halving`
- **定义**：距上次/下次 BTC 减半的天数。减半固定每 ~1458 天发生。
- **历史阶段（粗略）**：
  - 0-180d 减半后早期 — 价格通常温和反弹
  - 180-540d 牛市主升期 — 历史顶部基本都在 18 个月内
  - 540-900d 顶部 / 分配期 — 警惕分布
  - 900-1260d 熊市 / 积累期 — 经典 DCA 时段
  - 1260-1458d halving 前期 — 反弹起步
- **失效**：2024 ETF 通过后周期可能被宏观流动性主导，时间相位仅作粗参考。

#### `sma_200w` / `sma_200w_dev`
- **定义**：200 周（1400 日）简单均线（"BTC 长期价格地板"）+ 当前价偏离百分比。
- **历史阈值**：
  - dev < 0 → BTC 跌破 200 周线，**罕见** — 仅 2015、2018 末、2020 3 月、2022 LUNA 危机出现，每次都是绝对底部区
  - dev 0~100% → 正常区
  - dev > 150% → 牛市末期（2017 12月、2021 4月、2021 11月）
- **可信度**：极高，是 BTC 最稳定的长期估值锚。
- **失效**：算法本身不会失效。但 200wSMA 会随每日推进上移，"低估"标准在变。

#### `mayer_multiple` (price / 200d SMA)
- **历史阈值**：
  - < 0.6 → 历史抄底（2015、2018、2020、2022）
  - 0.6-1.0 → 偏低估
  - 1.0-2.4 → 中性区间
  - > 2.4 → 顶部（2013、2017、2021 一波二波）
- **失效**：2024 后波动率压缩，2.4 顶可能不再触发（ETF 改变波动结构）。

#### `pi_cycle_top_ratio` (111dMA / 2×350dMA)
- **定义**：111 日均线 / (2 × 350 日均线)。≥1 触发 = 历史 100% 命中顶部信号。
- **历史命中**：2013-12, 2017-12, 2021-04（皆在 3 天内顶部）。
- **失效情形**：2024+ 减半周期开始压缩，可能给假信号或滞后。
- **关键**：触发 ≥1 时严肃看待，"接近触发"（0.85-1.0）要警惕但非卖出信号。

#### `phase` (cycle phase 启发式分类)
- **取值**：`accumulation` / `early_post_halving` / `markup` / `late_markup_or_top` / `distribution_risk` / `transition`
- **逻辑**：基于 `days_since_halving` + `sma_200w_dev` 简单组合。
- **可信度**：低，仅作粗略地图。**真正决策看具体指标**。

---

### 💰 Valuation — 估值

#### `ahr999`
- **定义**：(price / 200d-DCA-cost) × (price / fitted-fair-value)。本实现是 **改进版**（v1.5 升级）：
  1. DCA 成本走调和均值（修正九神原版算术均值错误）
  2. 长期估值线动态加权回归（避免 2018 老系数失效）
  3. 评分用动态分位数（不固定 0.45/1.2 阈值）
  4. Huber IRLS 抗 outlier
- **历史阈值（raw 数值）**：
  - < 0.45 → 极端抄底（仅 2015、2019 1月、2022 末出现过）
  - 0.45-0.8 → 低估 / 经典定投区
  - 0.8-1.2 → 合理估值
  - 1.2-2.0 → 高估
  - > 2.0 → 泡沫
- **失效**：2024 后机构需求结构变化，分位数评分会自动跟随；raw 阈值仅作历史参考。
- **联动**：与 Mayer 高度相关；与 MVRV 互补（MVRV 用链上成本，AHR 用价格 DCA）。

#### `mvrv_z_score` / `nupl` (Phase 2，**需付费源**)
- **状态**：CoinMetrics community API 已收紧，免费 tier 无 realized cap。需 Glassnode/CryptoQuant 付费 API key 才能填充。
- **若有数据**，关键阈值：
  - MVRV Z < 0 → 历史抄底（2018、2020、2022）
  - MVRV Z > 7 → 历史顶部
  - NUPL < 0 capitulation；NUPL 0-0.25 hope；0.25-0.5 optimism；0.5-0.75 belief；>0.75 euphoria

---

### ⛏️ Network — 网络

#### `hash_rate_ehs` (EH/s)
- **定义**：BTC 全网哈希率，长期一直上行。绝对值用于和历史比较。
- **历史阈值**：
  - 2024-04 减半时 ~600 EH/s
  - 2024 末 ~700 EH/s
  - 2025 ~900 EH/s
  - 2026 中 ~1000+ EH/s（当前历史峰值区）
- **意义**：长期上行 = 矿工对网络/价格的信心投票。

#### `hash_ribbons` (30d MA vs 60d MA)
- **取值**：`上行（矿工扩张）` / `交叉中` / `下行（矿工投降信号）`
- **可信度**：**极高**。下行（30 < 60）历史命中：2018-12、2020-03、2022-06、2024-08 — 每次都是大底部前 1-3 月
- **失效**：低概率，机制稳定。
- **失效情形**：监管事件（如 2021 中国矿工迁移）可能误触发。

#### `difficulty_change_pct`
- **定义**：上次难度调整百分比（每 2016 块调一次，约 14 天）。
- **阈值**：
  - < -7% 大幅下调 → 矿工投降，常发生在恐慌底部
  - -2% ~ +3% 正常
  - > +8% 大幅上调 → 算力 FOMO 入场，常见于牛市中段
- **联动**：与 hash_ribbons 同向。

#### `mempool_mb`
- **定义**：mempool 待打包字节数（MB）。
- **阈值**：
  - < 5 → 畅通（牛市后期、熊市常见）
  - 5-30 → 正常
  - 30-100 → 拥堵
  - > 100 → 极度拥堵（2017 末、2021 4月、铭文/Runes 热潮 2023-2024）
- **意义**：链上活跃度。极度拥堵常是顶部信号或风潮事件。

---

### 📊 Positioning — 杠杆 & 情绪

#### `funding_rate_pct` (永续合约资金费率，% / 8h)
- **阈值**：
  - < -0.01% 负值 → 多头不愿付费给空头 → **反转信号**（极端恐慌 / 已大跌之后）
  - -0.01% ~ 0.005% 正常
  - > 0.05% 多头拥挤 → 清算风险
  - > 0.1% 极度过热（2021 4月、2024 3月顶都见过）
- **联动**：funding < 0 + F&G < 25 + price 跌破均线 = 高概率短期底。

#### `oi_to_mc` (OI / 市值比)
- **阈值**：
  - < 0.015 杠杆松弛 → 行情可能蓄力
  - 0.015-0.025 中性
  - 0.025-0.035 偏拥挤
  - > 0.04 过度拥挤 → 任何利空触发清算瀑布

#### `fear_greed`
- **阈值**：< 20 极度恐慌（历史抄底）；> 80 极度贪婪（顶部信号）
- **可信度**：中。极端值（<20 或 >80）较有意义，中间值噪音。
- **联动**：与 funding rate / 价格 三角验证。

#### `altcoin_season`
- **定义**：Top 50 代币中过去 90 天跑赢 BTC 的百分比（0-100）。自算 — 基于 Binance Top 50 kline，与 blockchaincenter.net 定义一致，无需外部 API。
- **阈值**：
  - ≥ 75 山寨季 — 资金溢出 BTC → Alt
  - 50-75 偏山寨季
  - 25-50 偏 BTC 季
  - < 25 BTC 季 — 资金集中 BTC
- **可信度**：中高。区块链中心实时计算，每日更新。
- **联动**：与 ETH/BTC 比率 + BTC Dominance 交叉验证。山寨季 > 75 + ETH/BTC 上行 + BTC Dom 下降 = Alt 风险偏好回归。

---

### 🌍 Macro — 宏观（FRED）

需要 `FRED_API_KEY` 环境变量。无 key 时该 domain 全部为 placeholder。

#### `dxy_60d_trend_pct` (DTWEXBGS 60 日变化 %)
- **定义**：Trade-Weighted USD Index (Broad)，FRED 上 DXY 的最佳代理（真 ^DXY 不在 FRED）。
- **阈值**：
  - < -3% 美元大幅走弱（BTC 顺风）
  - -3% ~ -1% 走弱
  - -1% ~ +1% 横盘
  - +1% ~ +3% 走强
  - > +3% 大幅走强（BTC 逆风）
- **联动**：与 BTC 价格历史负相关明显（2022 美元强势 = BTC 大熊）。

#### `real_yield_10y_pct` (DFII10)
- **定义**：10 年期 TIPS 实际利率（已是百分比）。
- **阈值**：
  - < 0% 负实际利率（极度宽松，2020-2022 风险资产狂飙时期）
  - 0% ~ 1% 低位（BTC 顺风）
  - 1% ~ 2% 正常
  - 2% ~ 2.5% 高位（BTC 逆风，2023-2024 多数时间）
  - > 2.5% 历史性逆风
- **机制**：实际利率 = 持有现金的真实回报。它越高，无风险资产相对吸引力越强，BTC/股市估值压缩。

#### `m2_yoy` (M2SL 同比 %)
- **定义**：M2 货币供应量同比增速（季调，月度数据）。
- **阈值**：
  - < -1% 收缩（罕见，2008、2022 末出现过）
  - -1% ~ 2% 停滞（流动性紧）
  - 2% ~ 5% 温和扩张
  - 5% ~ 8% 扩张（BTC 顺风）
  - > 8% 强劲扩张（2020-21 印钞峰值，BTC 翻数倍背景）
- **数据时效**：M2SL 月度发布，通常滞后 30-45 天。as_of 日期看 note 字段。

#### `spx_correlation_30d` (BTC vs SPX 30 日 Pearson 相关)
- **定义**：BTC 与 S&P 500 过去 30 个 SPX 交易日对数收益率的 Pearson 相关系数 [-1, 1]。
- **阈值**：
  - < -0.3 负相关（BTC 走独立避险行情，罕见）
  - -0.3 ~ +0.2 弱相关（BTC 独立性较强，是好 diversifier）
  - +0.2 ~ +0.5 中等相关
  - +0.5 ~ +0.7 强相关（BTC 表现像高 beta 科技股）
  - > +0.7 极强相关（与股市同步度极高，组合风险集中）

**启发**：2020 后 BTC 越来越像高 beta 风险资产，spx_correlation_30d 中位水平在 0.3-0.5。当 corr 突然飙到 >0.7，说明 BTC 已经被纳入"风险资产篮"由宏观流动性主导；当 corr 跌到 <0，往往是 BTC 出现独立叙事（如 ETF 通过、监管利好/利空）的时期。读盘必须把这 4 个指标和价格行为结合看。

---

### 💸 Flow — 资金流

#### `etf_net_flow_7d_usd` / `etf_net_flow_30d_usd`
- **定义**：现货 BTC ETF（IBIT, FBTC 等 11 只）净流入。来源 SoSoValue。
- **意义**：2024+ **最重要的边际需求驱动**。日均 $200M+ 持续流入支撑价格。
- **阈值（30d cumulative）**：
  - > $5B 强劲流入（机构 FOMO）
  - $1-5B 持续流入（正常牛市节奏）
  - $0-1B 微弱
  - < 0 流出（机构减持，BTC 逆风）
  - < -$3B 持续流出（前期顶部典型）
- **数据时效**：T-1 或 T-2（看 stale_days 字段）。

#### `etf_total_assets_usd`
- ETF 行业总持仓。$100B+ 表示机构覆盖深度。

#### `stablecoin_market_cap_usd` / `stablecoin_supply_30d_pct`
- **定义**：`stablecoin_market_cap_usd` = USDT+USDC+DAI+FDUSD+FRAX 总市值（CoinGecko 实时）。`stablecoin_supply_30d_pct` = 基于 history.db 采集的 30 日增速。
- **意义**：稳定币总市值 30 日增速 → 加密链上流动性。扩张 = 新钱进场。
- **注意**：`stablecoin_supply_30d_pct` 需要 history.db 攒够 ≥ 30 天稳定币市值数据才会出现。冷启动期间先看 `stablecoin_market_cap_usd` 绝对值。
- **阈值**（30d 增速）：
  - > +5% 强劲扩张
  - +1% ~ +5% 温和扩张
  - -3% ~ +1% 停滞
  - < -3% 收缩（流动性退潮）

#### `eth_btc_ratio`
- **定义**：ETH 价 / BTC 价。
- **解读**：
  - < 0.030 → ETH 极弱，资金避险偏 BTC
  - 0.030-0.045 → ETH 弱（典型熊市后期）
  - 0.045-0.060 → 中性
  - > 0.075 → ETH 强（风险偏好高 / Alt season 临近）

---

### 📈 Technical — 技术指标 (v3 新增)

#### `rsi_14`
- **定义**：14 日相对强弱指数 (0-100)。
- **阈值**：< 20 极度超卖；20-30 超卖；30-45 偏弱；45-55 中性；55-70 偏强；70-80 超买；> 80 极度超买。
- **联动**：RSI < 30 + MACD 空头收窄 + funding < 0 = 高概率短期底。

#### `macd_histogram`
- **定义**：MACD(12,26,9) 柱状图。>0 多头动能，<0 空头动能。
- **关键信号**：柱为负但收窄 = **底部反转信号**；柱为正但收窄 = 可能见顶。

#### `ema_cross` / `ma_alignment`
- `ema_cross` (EMA12 vs EMA26) → 短期趋势。>0 多头。
- `ma_alignment` (MA50 vs MA200) → 中期趋势。>0 金叉。
- **矛盾时**：EMA 多头 + MA 死叉 = 短多中空，横盘蓄势信号。

#### `bb_position` / `volatility_20d`
- BB(20,2) 位置 + 20 日波动率。两者同时极端低 = 变盘前兆（1-2 周内）。

---

### 🔗 CrossAsset — 跨资产对比 (v3 新增)

数据源：Yahoo Finance (GC=F / QQQ / SPY)。

#### `btc_gold_ratio` / `btc_qqq_ratio` / `btc_spy_ratio`
- **定义**：1 BTC = X oz 黄金 / X 股 QQQ / X 股 SPY。
- **btc_gold_ratio** 历史区间：~5（BTC 极弱）~ ~40（BTC 极强，2021 高点）。

#### `btc_gold_corr_30d` / `btc_qqq_corr_30d` / `btc_spy_corr_30d`
- **定义**：30 日对数收益率 Pearson 相关 [-1, 1]。
- **阈值**：< -0.3 负相关；-0.3~0.2 弱相关；0.2-0.5 中等；> 0.5 强相关；> 0.7 极强。
- **关键**：BTC 与 QQQ/SPY 相关骤升到 > 0.7 → BTC 被纳入"风险资产篮"，由宏观流动性主导。

#### `uup_price` / `btc_uup_corr_30d` (v3.1, Futu)
- **uup_price**：做多美元 ETF (Invesco DB USD Index Bullish Fund)。作为 DXY 的实时代理（FRED DTWEXBGS 延迟 1-3 天且需 API key）。来源 Futu US.UUP。
- **解读**：UUP 上涨 = DXY 走强 = 美元购买力上升 → BTC 以 USD 计价承压。历史相关性稳定为负。
- **btc_uup_corr_30d**：BTC vs UUP 30 日对数收益率 Pearson 相关。预期值应为 **负值**（-0.3 ~ -0.7）。
  - 若转正 → 反常信号，可能 BTC 与美元同涨（全球避险 + 美元避险同时发生），需警惕宏观极端事件。
- **联动**：UUP 走强 + 实际利率 > 2% + M2 收缩 → 三重美元逆风，BTC 压力最大。

#### `vixy_price` (v3.1, Futu)
- **定义**：VIX 短期期货 ETF，追踪 S&P 500 隐含波动率。传统市场"恐慌指数"。来源 Futu US.VIXY。
- **阈值**：< 15 极度平静（市场自满 → 警惕黑天鹅）；15-20 低波；20-25 正常；25-30 偏高（市场紧张）；> 30 极高恐慌 → 风险资产全面承压。
- **联动**：VIXY > 30 + F&G < 20 + funding < 0 = 全市场恐慌底，历史上是买入窗口（2020-03、2022-06）。
- **失效**：VIXY 跟踪的是美股波动，BTC 有时走独立行情（如 2024 ETF 通过时 VIXY 平稳但 BTC 暴涨）。需结合 `spx_correlation_30d` 判断独立性。

#### `gld_etf_price` (v3.1, Futu)
- **定义**：SPDR Gold Trust (GLD)，全球最大实物黄金 ETF。每份 ≈ 1/10 oz 黄金。来源 Futu US.GLD。
- **与 PAXG 的关系**：GLD × 10 ≈ PAXG（允许 ±2% 偏差，因 ETF 管理费和流动性差异）。两者同时可用时优先信 PAXG（币安 klines 与 BTC 时间戳对齐更好）；GLD 提供更长的历史（2011+，PAXG 仅 2019+）。
- **失效**：若 PAXG 与 GLD 偏离 > 5%，检查是否其中一方数据源异常（如 PAXG 流动性不足脱锚，或 GLD 美股休市）。

#### `rel_strength_90d_gold` / `rel_strength_90d_qqq`
- **定义**：BTC 90 日收益率 - 对方 90 日收益率（百分点差）。>0 BTC 跑赢。
- ⚠ 跨资产历史长度可能不对齐，极端值需结合比率交叉验证。

---

## 决策框架（Claude 输出操作建议时遵循）

### 信号计分规则

8 个域，每域统计看涨/看跌指标数，得出域级方向：

| 域 | 看涨条件 | 看跌条件 |
|----|---------|---------|
| Cycle | mayer < 1.0 或 sma_200w_dev < 0 | pi_cycle ≥ 0.85 或 sma_200w_dev > 150% |
| Valuation | ahr999 < 0.8 或 MVRV Z < 0 | ahr999 > 2.0 或 MVRV Z > 5 |
| Network | hash_ribbons 上行 或 difficulty > +5% | hash_ribbons 下行 + difficulty < -5% |
| Positioning | funding < 0 + OI/MC < 0.015 | funding > 0.05% + OI/MC > 0.035 |
| Macro | M2 > 5% + real_yield < 1% | real_yield > 2.5% + M2 < 0% |
| Flow | ETF 30d > $1B + ETH/BTC 上行 | ETF 30d < -$1B + 稳定币收缩 |
| Technical | MACD > 0 + RSI 30-70 | RSI > 80 或 RSI < 20 + MACD 继续下行 |
| CrossAsset | BTC/SPY corr < 0.3 + BTC 跑赢 QQQ | BTC/SPY corr > 0.7 + BTC 跑输 QQQ |

**计数**：每域看涨 = +1，看跌 = -1，中性/矛盾 = 0。总分范围 [-8, +8]。

### 操作建议映射

| 总分 | 建议 | 含义 |
|------|------|------|
| **≥ +5** | 🟢 **买入 (BUY)** | 5+ 域一致看涨。重仓买入。 |
| **+3 ~ +4** | 🔵 **加仓 (ACCUMULATE)** | 多数域看涨但非全部。定投加仓。 |
| **+1 ~ +2** | ⚪ **持有/观望 (HOLD)** | 略微偏多但信号不强。持仓不动。 |
| **0** | ⚪ **等待 (WAIT)** | 多空信号抵消。等待方向明朗。 |
| **-1 ~ -2** | 🟡 **减仓 (REDUCE)** | 略微偏空。分批减仓至 50%。 |
| **-3 ~ -4** | 🟠 **大幅减仓 (SELL HALF)** | 多数域看跌。减至 20-30% 仓位。 |
| **≤ -5** | 🔴 **卖出 (SELL)** | 5+ 域一致看跌。清仓或仅留底仓。 |

### 输出模板

每次分析必须包含：

```
## 操作建议: [BUY/ACCUMULATE/HOLD/WAIT/REDUCE/SELL HALF/SELL]

### 信号计分: +X / -8~+8
[列出每域的 +/-/0 和依据指标]

### 证据链
- 支持当前建议的 TOP 3 指标：
  1. [指标名 = 值 (q分位), 为什么支持]
  2. ...
  3. ...

### 反证
- 不支持当前建议的 TOP 2 指标：
  1. [指标名 = 值, 为什么矛盾/存疑]
  2. ...

### 失效条件
- 如果 [X 指标] 变化到 [阈值]，当前建议失效，应转为 [新建议]

### 概率权重
- 基准情景 (XX%): [描述]
- 替代情景 (XX%): [描述]
- 尾部风险 (XX%): [描述]
```

### 关键原则

1. **至少 3 域一致才有建议** — 单域信号不可单独决策
2. **q 分位 > label** — label 是静态阈值，q 是动态历史位置
3. **必须列出反证** — 每条建议必须有至少 2 个反证指标
4. **必须设失效条件** — 什么情况下这个建议就错了
5. **概率权重而非确定** — 用百分比而非"一定/绝对"
6. **不替代 trade-execution** — 仓位规模、止损位置交给 trade-execution skill

---

## 「读盘」工作流（Claude 应遵循）

不要从前到后读，而是按**决策影响顺序**：

### Step 1: 周期位置（地图坐标）
> "现在 BTC 大致在 cycle 哪一段？"

读 `days_since_halving` + `sma_200w_dev` + `phase`：
- accumulation / early_post_halving + dev<0% → 极端低估，DCA 黄金窗口
- markup + dev 0-100% → 中段，趋势跟随
- late_markup_or_top + dev>100% → 警惕分配
- distribution_risk + dev>150% → 接近顶部

### Step 2: 估值一致性（4 项交叉验证）
> "估值信号是否清晰？"

读 4 个估值指标：`ahr999`, `mayer_multiple`, `sma_200w_dev`, `pi_cycle_top_ratio`：
- **4 项都说低估** → 强烈低估，置信度高
- **4 项分歧** → 估值信号不清晰，等待
- **Pi Cycle 触发** → 顶部信号，独立看，权重最高

### Step 3: 网络健康（矿工是否在投降/扩张）
读 `hash_ribbons` + `difficulty_change_pct` + `mempool_mb`：
- 哈希率下行 + 难度大幅下调 → 矿工投降 = **底部前奏**
- 哈希率上行 + mempool 拥堵 → 链上活跃 = 牛市中-末

### Step 4: 杠杆健康（避免高拥挤区接刀）
读 `funding_rate_pct` + `oi_to_mc`：
- funding < 0 + OI 低 → 杠杆已洗，潜在反弹
- funding 高 + OI 高 → 杠杆拥挤，清算风险

### Step 5: 宏观（货币环境）
读 macro domain（4 个 FRED 指标）：
- `m2_yoy` 上行 + `dxy_60d_trend_pct` < 0 + `real_yield_10y_pct` 下行 → 流动性宽松，BTC 顺风
- 反之 → BTC 逆风
- `spx_correlation_30d` > 0.5 → BTC 主要受宏观流动性驱动（看股市判断方向）
- `spx_correlation_30d` < 0.2 → BTC 走独立行情（看链上 / ETF 流入判断）

### Step 6: 流入（边际新钱）
读 flow domain：
- ETF 持续正流入 + 稳定币扩张 + ETH/BTC 上行 → 风险偏好回归
- ETF 流出 + 稳定币收缩 → 流动性退潮

### Step 7: 技术指标（短期方向确认）
读 `rsi_14` + `macd_histogram` + `ma_alignment` + `bb_position` + `volatility_20d`：
- RSI < 30 + MACD 空头收窄 → 短期底
- RSI > 70 + MACD 多头减弱 → 短期顶
- MA 死叉 + EMA 多头 = 短多中空 → 横盘/方向待选
- BB 收窄 + 低波动 → 1-2 周内变盘

### Step 8: 跨资产（BTC 相对位置）
读 cross_asset domain：
- BTC/Gold 比率偏离历史区间 → 跨资产估值极端
- BTC 与 QQQ/SPY 30d 相关性骤升 → 宏观流动性主导
- BTC 独立行情（< 0.2 相关）+ ETF 持续流入 → BTC 自身叙事驱动

---

## 历史 Pattern 库（帮助 Claude 模式识别）

### 标准底部组合（典型抄底信号叠加）
✓ `sma_200w_dev` < 0
✓ `mayer_multiple` < 0.7
✓ `ahr999` < 0.45
✓ `hash_ribbons` 下行 → 即将上行（矿工投降转扩张）
✓ `funding_rate_pct` < -0.01% 持续多日
✓ `fear_greed` < 20
✓ `etf_net_flow_30d_usd` < 0（机构也在减持）
✓ `m2_yoy` 转正
**历史命中**：2018-12（$3.2k）、2020-03（$3.8k）、2022-12（$15.5k）、2024-08（$53k）

### 标准顶部组合（顶部分布信号）
✓ `pi_cycle_top_ratio` ≥ 1.0
✓ `mayer_multiple` > 2.4
✓ `sma_200w_dev` > 150%
✓ `funding_rate_pct` > 0.1% 持续
✓ `oi_to_mc` > 0.04
✓ `fear_greed` > 80 持续
✓ `mempool_mb` > 100
✓ `eth_btc_ratio` 急剧上行（资金溢出 BTC 涌向 alt）
**历史命中**：2013-12、2017-12、2021-04 一波、2021-11 二波

### 假信号案例（避免误判）
- **2019-04** AHR < 0.6 看似抄底，但 hash_ribbons 还在下行，price 又跌了 50% 才真正底部
- **2024-03 ETF 通过后** funding > 0.15% 看似顶部，但 ETF 持续流入 + M2 转正 = 假信号，接下来又涨 50%
- **2022-LUNA 崩盘** mvrv 失效短暂飙升，因 LUNA 链外事件污染。需要排除事件性偏差

---

## 反模式

- ❌ **看一个指标做决策** — 至少 3 个 domain 一致才有意义
- ❌ **从 4 个估值指标里挑利己的** — 4 个都看低估再说
- ❌ **忽略 stale_days 警告** — ETF 数据是 T-1，遇到周末/假期更滞后
- ❌ **盘面不写入交易日志就行动** — 用 trade-journal skill 留痕，事后才能复盘
- ❌ **黑天鹅时还看 观复** — 监管 / 交易所暴雷 / 协议漏洞超出指标范围，需立刻切换 news-dashboard
- ❌ **用 观复 决策 altcoin** — 仅覆盖 BTC + ETH/BTC 比率，山寨币用 cmc-mcp / okx-dex
- ❌ **看到 phase=accumulation 就 ALL IN** — phase 是粗分类，必须配合具体指标 + 仓位管理（trade-execution）
- ❌ **输出建议但不列反证** — 每条 BUY/SELL 建议必须有 ≥ 2 个反证指标
- ❌ **用绝对语气** — "一定会涨"、"绝对是底" → 正确表述："概率约 X%，基于 A/B/C 三个域一致看涨"
- ❌ **不设失效条件** — 必须声明"如果 X 指标变为 Y，本建议失效"

---

## 联动其他 skill

| 协同 skill | 用法 |
|---|---|---|
| `cmc-mcp` | 实时 BTC/ETH/F&G 数据，观复 是历史 + 周期 |
| `market-pulse` | 跨资产宏观 MHS，与 BTC 局部信号交叉验证 |
| `okx-dex` | altcoin/memecoin（观复 不覆盖）|
| `news-dashboard` | 黑天鹅事件 / 监管 / 交易所新闻 |
| `polymarket` | 加密相关预测市场（btc 价 / ETF 通过率 / 监管） |
| `trade-execution` | 观复 给方向 → 决定仓位 + 止损 |
| `trade-journal` | 决策时 log，事后复盘 |
| `valuation`（贵金属模式）| BTC 与黄金互补避险，跨资产估值 |

---

## 演进历史 / 设计决策

| 项目 | v1 (废弃) | v2 | v3 (当前) |
|---|---|---|---|
| 总分 0-100 | ❌ 设计性稀释，永远停在 50 附近 | 不输出 |
| Action / State | ❌ 硬编码阈值 = 假精度 | 不输出，由 Claude 综合 |
| 4 维 sigmoid 子分 | ❌ 隐式压缩 | → 6 域原始指标 + 历史分位 |
| 数据源 | BTC + ETH + Top50 + USDT/CNY | BTC + mempool + ETF + F&G + FRED + 极简 ETH |
| 估值层 | RSI + AHR | AHR + Mayer + 200wSMA dev + Pi Cycle |
| 网络层 | 无 | hash rate + ribbons + difficulty + mempool |
| 资金流 | 仅稳定币 | ETF 7d/30d + 稳定币 + ETH/BTC |
| 宏观层 | 无 | DXY + 10Y real yield + M2 YoY + BTC/SPX 30d corr (FRED) |
| 历史分位 | 无 | SQLite 每日采集 14 个指标 → 730 天分位 |
| 技术域 | 无 | 无 | RSI/MACD/EMA/MA/BB/波动 (7 指标) |
| 跨资产 | 无 | 无 | Gold/QQQ/SPY/UUP(DXY)/VIXY/GLD + 相关性 + 相对强弱 (12 指标) |
| 山寨季 | ❌ (1-btc_dom)×100 | blockchaincenter.net → 自算 | 自算 (基于 Top50 kline) |
| 黄金数据 | 无 | 无 | Binance PAXG + Futu GLD 双源交叉验证 |
| 美元指数 | 无 | FRED DTWEXBGS (需 key, 延迟) | Futu UUP 实时 + FRED 备用 |

| 决策依据 | 1 个 score / 1 个 action | 6 域指标盘 + 本手册 + Claude 综合 |
