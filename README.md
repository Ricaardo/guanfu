# 观复 (guanfu) — BTC 投资盘面

> 致虚极，守静笃。**万物并作，吾以观复。**
> ——《道德经》第十六章

guanfu 是一个 BTC 投资盘面 CLI 工具，输出 6 个域 30+ 指标的纯数据盘面。每个指标包含原始值、历史分位、解读标签和数据源。**不输出评分 / action / state** — 决策由人和 Claude 综合完成。

## 设计哲学

```
┌─────────────────────────────────────┐
│  决策层 (Human + Claude)             │
│  综合盘面 + 知识库 + 持仓/风险上下文  │
├─────────────────────────────────────┤
│  解读层 (SKILL.md)                   │
│  指标语义、历史阈值、组合 pattern    │
├─────────────────────────────────────┤
│  盘面层 (guanfu 二进制)              │
│  数据采集 → 指标计算 → 历史分位      │
└─────────────────────────────────────┘
```

- **万物并作** → 6 域指标同时呈现
- **观复** → 在历史分位中观察往复回归
- **致虚守静** → 工具不替代人的判断

## 安装

```bash
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest
```

或从源码构建：

```bash
git clone https://github.com/Ricaardo/guanfu.git
cd guanfu
make build
```

## 快速开始

```bash
# 完整盘面
guanfu

# JSON 输出（喂 Claude / 程序）
guanfu --json | jq

# 仅看估值域
guanfu --domain valuation

# AHR999 半衰期调整
guanfu --halflife 730
```

## 6 域指标

| 域 | 指标数 | 核心指标 |
|----|--------|----------|
| 🌊 Cycle 周期 | 7 | halving 天数、200wSMA 偏离、Mayer Multiple、Pi Cycle Top |
| 💰 Valuation 估值 | 4 | AHR999（改进版）、MVRV、MVRV Z-Score、NUPL |
| ⛏️ Network 网络 | 4 | 哈希率、Hash Ribbons、难度调整、Mempool 拥堵 |
| 📊 Positioning 杠杆 | 5 | 资金费率、OI/MC、恐慌贪婪、山寨季指数 |
| 🌍 Macro 宏观 | 4 | DXY 60d 趋势、10Y TIPS、M2 同比、SPX 相关性 |
| 💸 Flow 资金流 | 6 | ETF 7d/30d 净流入、稳定币市值及增速、ETH/BTC |

## 环境变量

| 变量 | 作用 |
|------|------|
| `FRED_API_KEY` | FRED 宏观数据（无 key 则 Macro 域为 placeholder） |
| `COINMETRICS_API_KEY` | CoinMetrics 付费端点 |
| `GUANFU_NO_HISTORY=1` | 禁用 history.db |
| `CACHE_DIR` | 缓存目录（默认 `./cache`） |

## 历史分位系统

ETF、mempool、资金费率等指标没有公开历史 API。guanfu 通过 SQLite (`~/.guanfu/history.db`) 每天采集一行，攒够 30 天后开始回填 `q`（历史分位）字段。

- 第 1 天：15 个指标入库，无 q 显示
- 第 30 天：开始显示 q
- 第 365 天：覆盖全年节奏
- 第 730 天：达到查询窗口上限

## 数据源

| 数据源 | 用途 | 免费 |
|--------|------|------|
| Binance | BTC/ETH K线 (3000d)、Top50、资金费率、OI | ✅ |
| CoinGecko | 总市值、BTC 市占率、稳定币市值 | ✅ |
| mempool.space | 哈希率 (3y)、难度、mempool | ✅ |
| SoSoValue | BTC ETF 净流入 | ✅ |
| alternative.me | 恐慌贪婪指数 | ✅ |
| CoinMetrics | MVRV/NUPL/MVRV Z | ✅ 社区端点 |
| FRED | DXY/10Y TIPS/M2/SPX | 需注册(免费) |

## 关键算法

### AHR999（改进版）

```
ahr999 = (price / DCA_200d) × (price / fair_value)

DCA_200d = 200 / Σ(1/price_i)         ← 调和均值（定投成本）
fair_value = exp(α + β × log(age))    ← Huber IRLS 动态拟合
```

评分使用同一窗口内 AHR 分布的动态分位数（q10/q35/q55/q75/q90），不固定 0.45/1.2。

### 山寨季指数

基于 Binance Top 50 kline **自算**：90 日跑赢 BTC 的币占比 × 100。不依赖外部 API。

### MVRV Z-Score

使用 rolling 1-year std(market_cap - realized_cap)，非全历史标准差。

## 项目结构

```
guanfu/
├── cmd/guanfu/          # CLI 入口
├── internal/
│   ├── client/          # 12+ 数据源并发拉取
│   ├── engine/          # 指标计算 + 盘面构建
│   ├── history/         # SQLite 历史分位
│   ├── model/           # 数据类型
│   ├── mathutil/        # 技术指标 (MA/EMA/MACD/RSI)
│   └── cache/           # 磁盘缓存
├── docs/                # 文档
├── Makefile
└── bin/                 # 编译输出
```

## License

MIT
