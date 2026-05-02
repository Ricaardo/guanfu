# 观复 (btc-guanfu): BTC 投资盘面系统

> 致虚极，守静笃。**万物并作，吾以观复。**
> ——《道德经》第十六章

## 目录

- [设计哲学](#设计哲学)
- [架构概览](#架构概览)
- [8 域指标体系](#8-域指标体系)
- [使用指南](#使用指南)
- [历史分位系统](#历史分位系统)
- [数据源矩阵](#数据源矩阵)
- [读盘工作流](#读盘工作流)
- [反模式](#反模式)
- [关键算法详解](#关键算法详解)
- [Skill 联动](#skill-联动)
- [v1 → v2 → v3 演进历史](#v1--v2--v3-演进历史)

---

## 设计哲学

### 核心隐喻

观复出自老子第十六章，用三层映射定义了这个系统的行为边界：

| 老子原文 | 系统映射 |
|----------|----------|
| **万物并作** | 8 个 domain × 42 个指标（周期 / 估值 / 网络 / 杠杆 / 宏观 / 资金流 / 技术 / 跨资产）同时呈现 |
| **观复** | 在每个指标的历史分位中看它往复回归 — halving 周期回环、估值高低反覆、流入流出来去 |
| **致虚守静** | 不输出评分 / action / state。工具守"静"，决策权归于人 |

### 盘 - 解读 - 决策三层分离

这是 v2 最核心的设计决策：

```
┌─────────────────────────────────────────────────┐
│  决策层 (Human + Claude)                          │
│  综合盘面 + 本手册 + 持仓/风险偏好/宏观上下文       │
├─────────────────────────────────────────────────┤
│  解读层 (SKILL.md)                                │
│  每个指标的语义、历史阈值、组合 pattern、失效情形   │
├─────────────────────────────────────────────────┤
│  盘面层 (btc-guanfu 二进制)                        │
│  数据采集 → 指标计算 → 历史分位 → 纯指标输出       │
└─────────────────────────────────────────────────┘
```

**v1 的错误**：前身 `coinman` 用 1 个 0-100 总分 + 6 类 action 试图替代综合判断。把多维结构压成 1 维必然丢失信息，阈值拍脑袋、过拟合历史。

**v2 的原则**：
- 每个指标输出**原始值**（无 sigmoid，无 scaling 隐式压缩）
- `q`（历史分位）告诉 Claude 当前值在过去分布中的位置
- `label` 仅用于人类速览，**不应作为决策依据**
- 不输出评分 / action / state

---

## 架构概览

```
cmd/guanfu/main.go             ← CLI 入口 (人类表格 / JSON 输出)
         │
    ┌────┴────┐
    │         │
    v         v
engine/calculator.go    engine/panel.go
  (v1 评分，废弃中)      (v3 指标盘 BuildPanel, 8 域 42 指标)
         │                    │
         └────────┬───────────┘
                  │
          client/real.go      ← 13 个数据源并发拉取
          client/mempool.go   ← mempool.space 网络数据
          client/coinmetrics.go ← 链上估值
          client/cross_asset.go ← 跨资产 (Binance PAXG + Futu/Yahoo)
          client/futu.go      ← 富途 OpenD Go 客户端 (自写 protobuf)
          client/futu_bridge.go ← Python bridge (官方 SDK 加密握手)
                  │
          history/store.go    ← SQLite 每日采集 → 历史分位
                  │
          model/types.go      ← MarketSnapshot / IndicatorPanel
```

### 文件清单

| 路径 | 职责 |
|------|------|
| `cmd/btc_guanfu/main.go` | CLI：flag 解析、人类表格 / JSON 输出 |
| `pkg/coinman/client/real.go` | 数据采集：Binance、CoinGecko、mempool、SoSoValue、CoinMetrics、FRED |
| `pkg/coinman/client/mempool.go` | mempool.space：哈希率、难度、mempool 拥堵 |
| `pkg/coinman/client/coinmetrics.go` | CoinMetrics：MVRV、NUPL、MVRV Z-Score |
| `pkg/coinman/engine/panel.go` | BuildPanel：6 域指标计算 + 历史分位回填 + label 函数 |
| `pkg/coinman/engine/calculator.go` | v1 评分引擎（向后兼容 NewsEngine 推送） |
| `pkg/coinman/history/store.go` | SQLite 历史存储：Record / QuantileAsOf / ValueAt |
| `pkg/coinman/mathutil/ma.go` | 技术指标：MA、EMA、MACD、RSI、Sigmoid、局部极值 |
| `pkg/coinman/model/types.go` | 数据模型：MarketSnapshot、IndicatorPanel、Indicator |
| `pkg/coinman/cache/cache.go` | 磁盘缓存：当日快照序列化，避免重复拉取 |
| `~/.claude/skills/btc-guanfu/SKILL.md` | 知识库：指标语义、历史阈值、pattern 库、失效情形 |

---

## 8 域指标体系

每个指标包含 6 个字段：

| 字段 | 含义 |
|------|------|
| `value` | 原始数值（无 sigmoid/scaling） |
| `q` | 历史分位 [0, 1]。q05 = 历史最低 5%，q95 = 历史最高 5%。需 ≥ 30 天 history.db 样本才显示 |
| `label` | 简短解读标签（仅人类速览，Claude 不应直接信赖） |
| `source` | 数据源标识 |
| `updated_at` | 数据时间 (RFC3339) |
| `note` | 计算备注 / 时效性提示 |

### 🌊 Cycle — 周期定位

| 指标 | 定义 | 关键阈值 |
|------|------|----------|
| `days_since_halving` | 距上次减半天数 | 0-180d 早期；180-540d 主升；540-900d 顶部/分配；900-1260d 熊市积累 |
| `days_to_halving` | 距下次减半天数（估计） | — |
| `sma_200w` | 200 周 (1400 日) SMA — BTC 长期价格地板 | 跌破 = 历史仅 5 次，每次都是绝对底部 |
| `sma_200w_dev` | (price - 200wSMA) / 200wSMA | < 0 深度低估；> 1.5 牛市末期 |
| `mayer_multiple` | price / 200d SMA | < 0.6 抄底；0.6-1.0 偏低估；1.0-2.4 中性；> 2.4 顶部 |
| `pi_cycle_top_ratio` | 111dMA / (2 × 350dMA) | ≥ 1 触发 = 历史 100% 命中顶部（2013-12, 2017-12, 2021-04） |
| `phase` | 启发式阶段分类 | accumulation / markup / distribution_risk / transition |

### 💰 Valuation — 估值

| 指标 | 定义 | 关键阈值 |
|------|------|----------|
| `ahr999` | (price / 200d-DCA-cost) × (price / fitted-fair-value) | < 0.45 极端抄底；0.45-0.8 定投区；0.8-1.2 合理；> 2.0 泡沫 |
| `mvrv` | Market Cap / Realized Cap | < 0.8 底部；1.2-2.0 中性；> 3.5 过热 |
| `mvrv_z_score` | (market_cap - realized_cap) / rolling_1y_std(mcap - rcap) | < 0 抄底；> 7 顶部 |
| `nupl` | (market_cap - realized_cap) / market_cap | < 0 capitulation；0.25-0.5 optimism；> 0.75 euphoria |

**AHR999 改进要点**（v1.5 升级）：
1. DCA 成本用**调和均值**（修正原版算术均值错误）
2. 长期估值线**动态加权回归**（Huber IRLS 抗 outlier，不沿用 2018 旧系数）
3. 评分阈值用**动态分位数**（不固定 0.45/1.2）
4. 半衰期可配置（`--halflife`）

### ⛏️ Network — 网络

| 指标 | 定义 | 关键阈值 |
|------|------|----------|
| `hash_rate_ehs` | 全网哈希率 (EH/s) | 长期上行 = 矿工对网络投票 |
| `hash_ribbons` | 30d MA vs 60d MA 交叉 | **下行 = 矿工投降，每次都是大底部前 1-3 月** |
| `difficulty_change_pct` | 上次难度调整 % | < -7% 投降信号；> +8% 算力 FOMO |
| `mempool_mb` | 待打包字节数 (MB) | < 5 畅通；> 100 极度拥堵（顶部常见） |

### 📊 Positioning — 杠杆 & 情绪

| 指标 | 定义 | 关键阈值 |
|------|------|----------|
| `funding_rate_pct` | 永续合约资金费率 (%/8h) | < -0.01% 反转信号；> 0.05% 多头拥挤；> 0.1% 过热 |
| `oi_to_mc` | OI(USD) / BTC 市值 | < 0.015 杠杆松弛；> 0.04 清算风险 |
| `fear_greed` | 恐慌贪婪指数 (0-100) | < 20 极度恐慌（抄底）；> 80 极度贪婪（顶部） |
| `altcoin_season` | Top 50 中 90 日跑赢 BTC 的占比 | > 75 山寨季；< 25 BTC 季。**自算，不依赖外部 API** |

### 🌍 Macro — 宏观

需 `FRED_API_KEY` 环境变量。无 key 时该域全部为 placeholder。

| 指标 | 定义 | 关键阈值 |
|------|------|----------|
| `dxy_60d_trend_pct` | 贸易加权美元指数 60 日变化 % | < -3% 美元走弱（BTC 顺风）；> +3% 走强（逆风） |
| `real_yield_10y_pct` | 10Y TIPS 实际利率 % | < 0% 极度宽松；> 2.5% 历史性逆风 |
| `m2_yoy` | M2 货币供应同比 % | < -1% 收缩；> 8% 强劲扩张（BTC 翻数倍背景） |
| `spx_correlation_30d` | BTC vs SPX 30 日 Pearson 相关 | > 0.7 强风险资产联动；< 0 走独立行情 |

### 💸 Flow — 资金流

| 指标 | 定义 | 关键阈值 |
|------|------|----------|
| `etf_net_flow_7d_usd` | 现货 BTC ETF 7 日净流入 | 日均 $200M+ 持续流入支撑价格 |
| `etf_net_flow_30d_usd` | 30 日累计净流入 | > $5B 机构 FOMO；< -$3B 持续流出（顶部） |
| `etf_total_assets_usd` | ETF 总持仓 USD | $100B+ 表示机构覆盖深度 |
| `stablecoin_market_cap_usd` | 稳定币总市值 | 绝对值，每日入库供后续计算增速 |
| `stablecoin_supply_30d_pct` | 稳定币市值 30 日增速 | > +5% 强劲扩张；< -3% 收缩 |
| `eth_btc_ratio` | ETH / BTC 价格比 | < 0.030 ETH 极弱（避险 BTC）；> 0.075 ETH 强（风险偏好高） |

---

## 使用指南

### CLI

```bash
# 完整盘面（人类可读表格）
btc-guanfu

# JSON 输出（喂 Claude / 程序）
btc-guanfu --json | jq

# 仅看一个 domain
btc-guanfu --domain cycle
btc-guanfu --domain valuation

# AHR999 拟合半衰期（默认 1460 = 4 年）
btc-guanfu --halflife 730   # 2 年，对快牛快熊敏感

# 自定义 history.db 路径
btc-guanfu --history-db /path/to/custom.db

# 调整拉数据超时
btc-guanfu --timeout 180s
```

### 环境变量

| 变量 | 作用 |
|------|------|
| `FRED_API_KEY` | FRED 宏观数据（无 key 则 Macro 域全 placeholder） |
| `COINMETRICS_API_KEY` | CoinMetrics 付费端点（免费 tier 也有社区端点可用） |
| `GUANFU_NO_HISTORY=1` | 禁用 history.db 写入/查询 |
| `CACHE_DIR` | 磁盘缓存目录（默认 `./cache`） |

### 冷启动

首次运行 ~5-8s（拉 Binance + mempool + SoSoValue + F&G + CoinGecko Top50），后续缓存命中 < 1s。

### 重建二进制

```bash
cd ~/news && go build -o bin/btc-guanfu ./cmd/btc_guanfu
```

---

## 历史分位系统

### 设计动机

ETF / mempool / 资金费率 / 宏观这类指标没有公开历史 API。观复通过 **SQLite 历史表** 自己每天采集一行，攒够样本后才能回填 `q` 字段。

BTC 价格相关指标（`sma_200w_dev`、`mayer_multiple`、`eth_btc_ratio` 等）的 `q` 由 Binance kline 历史直接计算，**不进 history.db**。

### 采集指标 (15 个)

**Flow**: `etf_net_flow_7d_usd`, `etf_net_flow_30d_usd`, `etf_total_assets_usd`, `stablecoin_market_cap_usd`, `stablecoin_supply_30d_pct`

**Network**: `mempool_mb`, `hash_rate_ehs`, `difficulty_change_pct`

**Positioning**: `funding_rate_pct`, `oi_to_mc`, `fear_greed`

**Macro**: `dxy_60d_trend_pct`, `real_yield_10y_pct`, `m2_yoy`, `spx_correlation_30d`

### q 分位生命周期

- 第 1 天：15 个指标入库，无 `q` 显示
- 第 30 天：开始显示 `q`（样本 ≥ 30）
- 第 365 天：`q` 完全有意义（覆盖全年节奏）
- 第 730 天：达到查询窗口上限（2 年），老数据不参与分位计算

### 直接查询

```bash
sqlite3 ~/.guanfu/history.db \
  "SELECT date, value FROM daily_metrics WHERE key='etf_net_flow_30d_usd' ORDER BY date DESC LIMIT 30"
```

### 路径

- 默认：`~/.guanfu/history.db`
- 兼容老路径：`~/.coinman/history.db`（可手动 mv 迁移）
- 可通过 `--history-db` 或 `GUANFU_NO_HISTORY=1` 覆盖

---

## 数据源矩阵

| 数据源 | 用途 | 需要 Key | 免费 tier |
|--------|------|----------|-----------|
| Binance | BTC/ETH 价格 + K 线历史 (3000d) + Top50 历史 + 资金费率 + OI | 否 | 公开 |
| CoinGecko | 总市值 + BTC 市占率 + 稳定币市值 | 否 | 公开（有频率限制） |
| mempool.space | 哈希率 (3y) + 难度调整 + mempool 拥堵 | 否 | 公开 |
| SoSoValue | 现货 BTC ETF 净流入 (7d/30d) + 总持仓 | 否 | 公开 |
| alternative.me | 恐慌贪婪指数 | 否 | 公开 |
| CoinMetrics | MVRV / NUPL / MVRV Z-Score | `COINMETRICS_API_KEY` | 社区端点可用 |
| FRED | DXY / 10Y TIPS / M2 / SPX | `FRED_API_KEY` | 需注册(免费) |
| Blockchain Center | ~~山寨季指数~~ → **已改为自算** | 否 | 不再依赖 |

---

## 读盘工作流

不要从前到后读，按**决策影响顺序**：

### Step 1: 周期位置（地图坐标）

读 `days_since_halving` + `sma_200w_dev` + `phase`：
- accumulation / early_post_halving + dev < 0 → 极端低估，DCA 黄金窗口
- markup + dev 0-100% → 中段，趋势跟随
- distribution_risk + dev > 150% → 接近顶部

### Step 2: 估值一致性（4 项交叉验证）

读 `ahr999` + `mayer_multiple` + `sma_200w_dev` + `pi_cycle_top_ratio`：
- **4 项都说低估** → 强烈低估，置信度高
- **4 项分歧** → 估值信号不清晰，等待
- **Pi Cycle 触发** → 顶部信号，独立看，权重最高

### Step 3: 网络健康（矿工是否在投降/扩张）

读 `hash_ribbons` + `difficulty_change_pct` + `mempool_mb`：
- 哈希率下行 + 难度大幅下调 → 矿工投降 = **底部前奏**
- 哈希率上行 + mempool 拥堵 → 链上活跃 = 牛市中-末

### Step 4: 杠杆健康（避免高拥挤区接刀）

读 `funding_rate_pct` + `oi_to_mc` + `fear_greed`：
- funding < 0 + OI 低 → 杠杆已洗，潜在反弹
- funding 高 + OI 高 + F&G > 80 → 杠杆拥挤，清算风险

### Step 5: 宏观（货币环境）

读 macro domain：
- m2_yoy 上行 + dxy 走弱 + real_yield 下行 → 流动性宽松，BTC 顺风
- spx_correlation_30d > 0.5 → BTC 受宏观流动性主导
- spx_correlation_30d < 0.2 → BTC 走独立行情（看链上 / ETF）

### Step 6: 流入（边际新钱）

读 flow domain：
- ETF 持续正流入 + 稳定币扩张 + ETH/BTC 上行 → 风险偏好回归
- ETF 流出 + 稳定币收缩 → 流动性退潮

### 标准底部组合

✓ `sma_200w_dev` < 0 ✓ `mayer_multiple` < 0.7 ✓ `ahr999` < 0.45 ✓ `hash_ribbons` 下行 ✓ `funding_rate_pct` < -0.01% ✓ `fear_greed` < 20 ✓ `m2_yoy` 转正

**历史命中**: 2018-12 ($3.2k), 2020-03 ($3.8k), 2022-12 ($15.5k), 2024-08 ($53k)

### 标准顶部组合

✓ `pi_cycle_top_ratio` ≥ 1.0 ✓ `mayer_multiple` > 2.4 ✓ `sma_200w_dev` > 150% ✓ `funding_rate_pct` > 0.1% ✓ `oi_to_mc` > 0.04 ✓ `fear_greed` > 80

**历史命中**: 2013-12, 2017-12, 2021-04, 2021-11

---

## 反模式

- ❌ **看一个指标做决策** — 至少 3 个 domain 一致才有意义
- ❌ **从 4 个估值指标里挑利己的** — 4 个都看低估再说
- ❌ **忽略 stale_days 警告** — ETF 数据是 T-1，遇到周末/假期更滞后
- ❌ **盘面不写入交易日志就行动** — 用 `trade-journal` skill 留痕
- ❌ **黑天鹅时还看观复** — 监管 / 交易所暴雷 / 协议漏洞超出指标范围
- ❌ **用观复决策 altcoin** — 仅覆盖 BTC + ETH/BTC 比率 + 山寨季指数
- ❌ **看到 phase=accumulation 就 ALL IN** — phase 是粗分类，必须配合具体指标
- ❌ **只看 label 不看 q** — label 阈值基于 2013-2024 周期，可能已过时

---

## 关键算法详解

### AHR999（自适应改进版）

```
ahr999 = (price / DCA_200d) × (price / fair_value)

其中:
  DCA_200d = 200 / Σ(1/price_i)        ← 调和均值（定投成本），非算术均值
  fair_value = exp(α + β × log(age))    ← 动态加权回归，非固定系数
```

**拟合流程**：
1. 取最近 8 年 BTC 日线，构造 (log-age, log-price) 样本
2. 时间衰减加权：`weight = 0.5^(i / halfLife)`，越近权重越大
3. 初始加权最小二乘 → α₀, β₀
4. Huber 单步重加权 (c=2×MAD) → 降 2021 顶/LUNA-FTX 极端样本权重
5. 最终 α, β → 计算 fair_value

**动态分位评分**：不在固定 0.45/1.2 阈值，而是在同一拟合窗口内取 log(AHR) 的 q10/q35/q55/q75/q90 分位映射到 [-1, 1]。

### MVRV Z-Score

```
Z = (market_cap - realized_cap) / rolling_1y_std(market_cap - realized_cap)
```

使用 **1 年滚动窗口标准差**（非全历史），避免长期市值增长稀释 Z 值。

### 山寨季指数

```
altcoin_season = (outperformed_count / total_count) × 100

其中:
  total_count = Top 50 中有 ≥ 90 天历史的币（排除稳定币/wrapped）
  outperformed = 90 日涨幅 > BTC 同期涨幅的币
```

**完全自算**，基于 Binance Top 50 kline，无需外部 API。与 BlockchainCenter 定义一致。

### Hash Ribbons

```
ma_30d = mean(hash_rate 最后 30 天)
ma_60d = mean(hash_rate 最后 180 天)  ← 用 180 天窗口使 MA 更稳定

diff = (ma30 - ma60) / ma60
> +2%   → 上行（矿工扩张）
< -2%   → 下行（矿工投降）
中间    → 交叉中
```

---

## Skill 联动

| 协同 skill | 用法 |
|------------|------|
| `cmc-mcp` | 实时 BTC/ETH/F&G 数据，观复是历史 + 周期 |
| `market-pulse` | 跨资产宏观 MHS，与 BTC 局部信号交叉验证 |
| `news-dashboard` | 黑天鹅事件 / 监管 / 交易所新闻 |
| `polymarket` | 加密相关预测市场（BTC 价 / ETF 通过率 / 监管） |
| `trade-execution` | 观复给方向 → 决定仓位 + 止损 |
| `trade-journal` | 决策时 log，事后复盘 |
| `valuation`（贵金属） | BTC 与黄金互补避险，跨资产估值 |
| `technical-analysis` | 补充技术面（观复专注周期/估值，不含 K 线形态分析） |

### 建议操作节奏

- **周一早晨**：market-pulse(MHS) → event-calendar(本周事件) → thesis-tracker(decay 检查) → portfolio-manager(偏离)
- **周日晚**：trade-journal(复盘) → thesis-tracker(更新) → market-pulse(趋势) → research(下周 idea)
- **每日**：观复盘面（5s 扫一遍 6 域指标变化）

---

## v1 → v2 → v3 演进历史

| 维度 | v1 (coinman) | v2 (观复) | v3 (当前) |
|------|-------------|----------|----------|
| 总分 | ❌ 0-100 设计性稀释 | 不输出 | 不输出 |
| Action/State | ❌ 硬编码阈值 = 假精度 | 不输出，Claude + 人综合 | 8 域 42 指标 + q 分位 + AI 综合 |
| 子分 | 4 维 sigmoid 隐式压缩 | 6 域原始指标 + 历史分位 | 8 域 42 原始指标 + 历史分位 |
| 数据源 | BTC + ETH + Top50 + USDT/CNY | + mempool + ETF + F&G + FRED + CoinMetrics | + Futu (6 assets) + Binance PAXG + Yahoo (backup) |
| 估值层 | RSI + AHR | AHR(改进版) + Mayer + 200wSMA + Pi Cycle + MVRV/NUPL | 同 v2 + 动态分位评分 |
| 网络层 | 无 | hash rate + ribbons + difficulty + mempool | 同 v2 + 180d MA 窗口 |
| 资金流 | 仅稳定币增速 | ETF 7d/30d + 稳定币市值 + 稳定币 30d 增速 + ETH/BTC | 同 v2 + real-time stablecoin cap |
| 宏观层 | 无 | DXY + 10Y real yield + M2 YoY + SPX corr (FRED) | + Futu UUP (DXY 实时代理) |
| 技术域 | 无 | 无 | RSI/MACD/EMA/MA50-200/BB/波动 (7 指标) |
| 跨资产域 | 无 | 无 | BTC vs Gold/QQQ/SPY/UUP/VIXY + correlations + rel strength (12 指标) |
| 历史分位 | 无 | SQLite 每日采集 15 个指标 → 730 天分位 | 同 v2 |
| 决策依据 | 1 个 score | 6 域盘面 + SKILL.md + Claude | 8 域盘面 + 520 行 SKILL.md + 8 步读盘法 + AI |
| 山寨季 | ❌ (1-btc_dom)×100 | blockchaincenter → 自算 | 自算 (Top50 kline, 零外部依赖) |
| 黄金 | 无 | 无 | Binance PAXG + Futu GLD 双源交叉验证 |
| DXY | 无 | FRED DTWEXBGS (需 key) | Futu UUP 实时 + FRED 备用 |
