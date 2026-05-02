# 观复 (guanfu) — BTC 投资盘面

> 致虚极，守静笃。**万物并作，吾以观复。**
> ——《道德经》第十六章

guanfu 是一个 BTC 投资盘面 CLI 工具，输出 **8 个域 44 指标**的纯数据盘面（攒满 30 天历史后 45）。每个指标包含原始值、历史分位、解读标签和数据源。**不输出评分 / action / state** — 决策由人和 AI 综合完成。

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

> **免责声明**：观复输出的是历史分位 + 模式参考，**不是投资建议**。BTC 价格高度波动，过去的指标 pattern 不保证未来重现。任何决策请结合自己的风险承受能力、持仓周期和资金状况，盈亏自负。

## 设计哲学

guanfu 出自《道德经》第十六章："致虚极，守静笃。万物并作，吾以观复。"

**三层分离**是核心设计决策：

```
┌──────────────────────────────────────────┐
│  决策层 (Human + Claude / ChatGPT)        │
│  8 域 44 指标 × q 分位 × 组合 pattern     │
│  → 概率性判断，不输出确定性结论            │
├──────────────────────────────────────────┤
│  解读层 (SKILL.md)                         │
│  每个指标的语义、历史阈值、失效情形、联动   │
├──────────────────────────────────────────┤
│  盘面层 (guanfu 二进制)                    │
│  多数据源 → 指标计算 → 历史分位 → JSON     │
└──────────────────────────────────────────┘
```

**为什么不是评分系统**：v1 (CoinMan) 曾用一个 0-100 总分 + 6 类 action 试图替代综合判断。把 44 个指标压成 1 个数字必然丢失信息——总分永远停在 50 附近，无法区分"杠杆过热导致的上涨"和"机构买入驱动的上涨"。v2 更名为"观复"：不输出评分，只输出原始指标 + 历史分位，由人和 AI 完成综合判断。

三层映射关系：
- **万物并作** → 8 域 44 指标同时呈现
- **观复** → 在 q 分位中观察每个指标的往复回归
- **致虚守静** → 工具守"静"，不替代主人的判断

## 安装

```bash
# 一行安装
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest
go install github.com/Ricaardo/guanfu/cmd/guanfu-similar@latest  # 可选：历史相似度

# 或源码构建
git clone https://github.com/Ricaardo/guanfu.git
cd guanfu && make all
```

**Futu Bridge（可选，获取 QQQ/SPY/DXY/VIX 实时数据）**：

```bash
pip install futu-api

# 推荐：放到 ~/.guanfu/ 下，guanfu 会自动找到（cron / launchd / 任意 PATH 都生效）
mkdir -p ~/.guanfu
curl -sL https://raw.githubusercontent.com/Ricaardo/guanfu/main/internal/client/futu_bridge.py \
  -o ~/.guanfu/futu_bridge.py

# 或放到二进制同目录 / ~/.config/guanfu/ / 通过 FUTU_BRIDGE 环境变量自定义
```

guanfu 按以下顺序查找 bridge：`$FUTU_BRIDGE` → 二进制同目录 → `~/.guanfu/` → `~/.config/guanfu/`。

## 使用

```bash
# 第一次运行（不写 history.db，最轻量）
GUANFU_NO_HISTORY=1 guanfu

# 完整盘面（人类可读，冷启动通常 60-90s，缓存命中 < 1s；首次会创建 ~/.guanfu/history.db）
guanfu

# JSON 输出 → 喂 Claude / ChatGPT
guanfu --json | jq

# 只看关心的域
guanfu --domain valuation    # 估值
guanfu --domain technical    # 技术指标
guanfu --domain cross_asset  # 跨资产对比

# 参数
guanfu --halflife 730        # AHR 半衰期（默认 1460=4年）
guanfu --timeout 180s        # 超时
guanfu --plain               # 纯文本输出（无 emoji / box drawing）
guanfu --version             # 打印版本（提 issue 时附上）
GUANFU_NO_HISTORY=1 guanfu   # 跳过 history.db
```

**配合 AI 使用**：

```bash
# Claude Code: 直接对话"BTC 现在怎么样？"（skill 自动调用）

# Claude API / ChatGPT: 将 JSON + SKILL.md 附加到 prompt
guanfu --json > panel.json
cat panel.json | your-ai-client --system "$(cat docs/SKILL.md)"
```

**历史相似度（推广/复盘用）**：

```bash
# 一次性比对
guanfu --json | guanfu-similar --top 8         # 默认 --history-dir ~/.guanfu/panels

# 每日 archive 一份盘面（cron / launchd）
mkdir -p ~/.guanfu/panels
guanfu --json > ~/.guanfu/panels/$(date -u +%F).json

# crontab 行（每天 09:00）
0 9 * * * /usr/bin/env -S bash -lc 'guanfu --json > ~/.guanfu/panels/$(date -u +%F).json 2>> ~/.guanfu/cron.log'
```

archive 攒满 30+ 天后，`guanfu-similar` 给出的"今天与历史最相似的盘面"才有统计意义。相似度只比较双方都有 `q` 的指标，方法见 [docs/backtest-methodology.md](docs/backtest-methodology.md)。公开文案里的历史收益数字必须由该流程生成，并披露样本数量、窗口和反例。

## Demo

```text
$ guanfu --plain
guanfu BTC panel (2026-05-02)   price: $78209.73
BTC dominance: 58.46%   F&G: 39   total cap: $2.7T

Cycle 周期定位
  days_since_halving             742  顶部 / 分配期 (18-30m)
  mayer_multiple              0.9325  q20  偏低估
  pi_cycle_top_ratio          0.3868  未触发
  sma_200w_dev                +29.42%  q81  正常区
  ...

Valuation 估值
  ahr999                      0.7118  q24  低估 / 定投区
  mvrv_z_score                1.2613  中性偏低
  ...

Network 网络
  hash_ribbons               下行（矿工投降信号 ⚠）
  ...
```

完整 8 域 44 指标见 [`docs/SKILL.md`](docs/SKILL.md)。

## 8 域指标体系

| 域 | 指标数 | 核心指标 |
|----|--------|----------|
| 🌊 Cycle 周期 | 7 | halving 天数、200wSMA 偏离、Mayer Multiple、Pi Cycle Top |
| 💰 Valuation 估值 | 4 | AHR999（改进版）、MVRV、MVRV Z-Score、NUPL |
| ⛏️ Network 网络 | 4 | 哈希率、Hash Ribbons、难度调整、Mempool 拥堵 |
| 📊 Positioning 杠杆 | 4 | 资金费率、OI/MC、恐慌贪婪、**山寨季指数（自算）** |
| 🌍 Macro 宏观 | 4 | DXY 60d 趋势、10Y TIPS、M2 同比、SPX 相关性 |
| 💸 Flow 资金流 | 5 (+1) | ETF 7d/30d 净流入、稳定币市值（30d 增速攒满 30 天后回填） |
| 📈 Technical 技术 | 8 | RSI(14)、MACD 柱、EMA 交叉、MA50/200、Bollinger、波动率 |
| 🔗 CrossAsset 跨资产 | 8 | BTC/Gold·QQQ·SPY/UUP/VIXY/GLD 比率、相关性、相对强弱 |

## 环境变量

| 变量 | 作用 |
|------|------|
| `FRED_API_KEY` | FRED 宏观数据（无 key 则 Macro 域为 placeholder） |
| `COINMETRICS_API_KEY` | CoinMetrics 付费端点 |
| `GUANFU_NO_HISTORY=1` | 禁用 history.db 写入/查询 |
| `GUANFU_HISTORY_DB` | MCP Server 使用的 history.db 路径（CLI 用 `--history-db`） |
| `GUANFU_SKILL_PATH` | MCP Resource `guanfu://knowledge/skill.md` 的 SKILL.md 路径 |
| `CACHE_DIR` | 缓存目录（默认 `./cache`） |
| `FUTU_GATEWAY` | 富途 OpenD 地址（默认 `127.0.0.1:11111`） |
| `FUTU_ENABLED=0` | 禁用富途，直接用 Yahoo 降级 |
| `FUTU_BRIDGE` | 自定义 futu_bridge.py 路径 |

## 历史分位系统

ETF、mempool、资金费率等指标没有公开历史 API。guanfu 通过 SQLite (`~/.guanfu/history.db`) 每天采集一行，攒够 30 天后回填 `q`（历史分位）字段。

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
| Yahoo Finance | GC=F (黄金)、QQQ、SPY (Futu 不可用时的降级) | ✅ |
| Futu OpenD | QQQ/SPY/GLD/UUP/VIXY (本地网关，需 Python bridge) | ✅ |
| FRED | DXY/10Y TIPS/M2/SPX | 需注册(免费) |

## AI 集成

guanfu 设计为 AI 原生工具，可接入多种 AI 平台：

| 平台 | 方式 | 状态 |
|------|------|------|
| **Claude Code** | `btc-guanfu` skill → 调用 CLI JSON | ✅ 已可用 |
| **Claude Desktop / Cursor** | MCP Server 封装 guanfu | ✅ 已可用 |
| **ChatGPT** | GPT Action → REST API | 📋 计划 |
| **任意 AI** | CLI JSON + System Prompt | ✅ 已可用 |

详细方案见 [docs/guanfu-ai-integration.md](docs/guanfu-ai-integration.md)。

## 关键算法

### AHR999（改进版）

```
ahr999 = (price / DCA_200d) × (price / fair_value)
DCA_200d = 200 / Σ(1/price_i)         ← 调和均值（非算术）
fair_value = exp(α + β × log(age))    ← Huber IRLS 动态拟合
```

评分使用动态分位数（q10/q35/q55/q75/q90），不固定 0.45/1.2 阈值。

### 山寨季指数（自算）

基于 Binance Top 50 kline：90 日跑赢 BTC 的占比 × 100。**零外部 API 依赖**。

### MVRV Z-Score

使用 rolling 1-year std(market_cap - realized_cap)，非全历史标准差。

### Hash Ribbons

30d MA vs 60d MA（180 天窗口），下行 = 矿工投降 = 历史抄底前兆。

## 项目结构

```
guanfu/
├── cmd/guanfu/          # CLI 入口
├── cmd/guanfu-mcp/      # MCP Server 入口
├── cmd/guanfu-similar/  # 历史 JSON 盘面相似度工具
├── .github/workflows/   # CI / release
├── internal/
│   ├── client/          # 多数据源并发拉取
│   ├── engine/          # 指标计算 + 8 域盘面构建
│   ├── history/         # SQLite 历史分位
│   ├── model/           # 数据类型
│   ├── mathutil/        # 技术指标 (MA/EMA/MACD/RSI/BB)
│   └── cache/           # 磁盘缓存
├── docs/                # 文档 + SKILL.md 知识库
├── Makefile
└── bin/                 # 编译输出
```

## License

MIT
