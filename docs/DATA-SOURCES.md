# 数据源与配置

> guanfu 的数据来源、获取方式、环境变量配置和降级策略。

---

## 一、数据源总览

### 免费数据源（开箱即用）

| 数据源 | 用途 | 频率 | 延迟 | 速率限制 |
|---|---|---|---|---|
| **CoinMetrics + Binance** | BTC 日线全历史（2010-07-18 至最新）：CoinMetrics `PriceUSD` 建全量，Binance 最新日线增量覆盖 | 每次未命中快照缓存时检查并更新 | CoinMetrics T-1；Binance 实时 | CoinMetrics 免费 tier 有限；Binance 1200 req/min |
| **Binance** | ETH K线 (3000d)、Top50 K线、资金费率、OI、当前价 | 每次运行；缓存命中最多 4 小时 | 实时 | 1200 req/min |
| **CoinGecko** | 总市值、BTC 市占率、稳定币市值、Top50 列表 | 每次运行；缓存命中最多 4 小时 | 实时 | 10-30 req/min (免费) |
| **mempool.space** | 哈希率、难度调整、mempool 拥堵 | 每次运行 | 实时 | 无明确限制 |
| **SoSoValue** | 现货 BTC ETF (IBIT/FBTC等) 净流入 7d/30d、总资产 | 每次运行 | T-1 或 T-2 | 未知 |
| **alternative.me** | 恐慌贪婪指数 (0-100) | 每次运行 | 实时 | 低 |
| **CoinMetrics** | BTC `PriceUSD` 全历史、MVRV、NUPL、MVRV Z-Score（社区端点） | 每次运行 | T-1 | 免费 tier 有限 |
| **Yahoo Finance** | QQQ/SPY fallback、CL=F WTI futures fallback | 每次运行 | 实时 | 低 (chart API) |

### 需注册的免费源

| 数据源 | 用途 | 注册方式 | 环境变量 |
|---|---|---|---|
| **FRED** (St. Louis Fed) | DTWEXBGS (DXY代理)、DFII10 (10Y TIPS)、M2SL (M2)、SP500、**BAMLH0A0HYM2 (HY利差)**、**T10Y2Y (10Y-2Y利差)** | https://fred.stlouisfed.org/docs/api/api-key.html | `FRED_API_KEY` |

### 本地网关

| 数据源 | 用途 | 部署方式 |
|---|---|---|
| **Futu OpenD** | QQQ、SPY、GLD (黄金ETF)、UUP (做多美元ETF)、TLT、VIXY、USO (油价 ETF proxy) | 下载 OpenD → 启动 → 本地 `127.0.0.1:11111` |

> 注意：`US.USO` 是原油 ETF proxy，不是 WTI $/桶；只有 Yahoo `CL=F` fallback 才按 WTI futures 解释。JSON 顶层 `source_health` 会标记 fallback 与数据源状态。

---

## 二、环境变量完整列表

### 数据源配置

```bash
# FRED 宏观（无 key 则 Macro 域全为 placeholder）
export FRED_API_KEY="your_32_char_key"

# CoinMetrics 付费端点（可选；免费社区端点已内置）
export COINMETRICS_API_KEY="..."

# 富途 OpenD
export FUTU_GATEWAY="127.0.0.1:11111"  # 默认地址
export FUTU_ENABLED=0                    # 设为 0 禁用富途，走 Yahoo 降级
export FUTU_BRIDGE="/path/to/futu_bridge.py"  # 自定义 bridge 路径
```

### 运行时配置

```bash
# 历史分位 DB
export GUANFU_HISTORY_DB="/custom/path/history.db"  # 仅 MCP Server；CLI 用 --history-db
export GUANFU_NO_HISTORY=1                           # 禁用 history.db 写入

# Skill 路径（MCP Server Resource）
export GUANFU_SKILL_PATH="~/.claude/skills/btc-guanfu/SKILL.md"

# 缓存
export CACHE_DIR="./cache"  # 可选覆盖；未设置时默认使用系统用户缓存目录
export GUANFU_BTC_KLINE_CACHE="$HOME/.guanfu/btc_daily_history.json"  # 可选：固定 BTC 全历史缓存路径
```

磁盘行情缓存按 `MarketSnapshot` schema 校验；schema 变化、BTC 历史不足 2010 至最新，或抓取时间超过 4 小时会自动重拉。BTC 日线另有增量缓存（默认 `$CACHE_DIR/btc_daily_history.json`，未设置 `CACHE_DIR` 时使用系统用户缓存目录；可用 `GUANFU_BTC_KLINE_CACHE` 覆盖），每次未命中快照缓存的运行中都会检查并更新最新日线；AHR999 每次由这份最新 BTC 历史重新计算。MCP 另有进程内缓存，可用 `guanfu-mcp --cache-ttl=5m` 调整。

### 命令行参数

```bash
guanfu --timeout 180s      # API 超时
guanfu --halflife 730       # AHR 拟合半衰期（默认 1460=4年）
guanfu --history-db /path   # 指定 history.db 路径
guanfu --plain              # 纯文本输出
guanfu --json               # JSON 输出
guanfu --domain valuation   # 只看一个域
```

---

## 三、数据拉取流程

### 启动时的并发拉取

```
                    ┌──────────────┐
                    │   guanfu     │
                    └──────┬───────┘
           ┌───────────────┼───────────────┐
           │               │               │
     ┌─────▼─────┐  ┌──────▼──────┐  ┌─────▼─────┐
     │ Binance    │  │ CoinGecko   │  │ Mempool   │
     │ BTC最新日线│  │ 市值/Dom    │  │ 哈希率等  │
     │ ETH K线    │  │ 稳定币      │  │           │
     │ Top50 K线  │  └─────────────┘  └─────────────┘
     │ 费率/OI    │
     └─────┬─────┘
           │
    ┌─────▼─────┐  ┌──────────────┐  ┌─────────────┐
    │ SoSoValue │  │ alternative  │  │ CoinMetrics │
    │ ETF 数据   │  │ F&G 指数    │  │ BTC历史/MVRV│
     └───────────┘  └──────────────┘  └─────────────┘
           │               │               │
     ┌─────▼─────┐  ┌──────▼──────┐  ┌─────▼─────┐
     │ FRED      │  │ Yahoo       │  │ Futu      │
     │ DXY/TIPS  │  │ Gold/QQQ/SP │  │ QQQ/SPY   │
     │ M2/SPX    │  │ (降级)      │  │ GLD/UUP   │
     └───────────┘  └─────────────┘  │ VIXY      │
                                     └───────────┘
```

### 降级策略

| 数据 | 优先 | 降级 1 | 降级 2 | 全部不可用 |
|---|---|---|---|---|
| DXY/TIPS/M2/SPX | Futu UUP + FRED | FRED only | — | Macro 域为 placeholder |
| MVRV/NUPL | CoinMetrics 付费 | CoinMetrics 社区 | — | 链上估值为 placeholder |
| QQQ/SPY | Futu | Yahoo Finance | — | CrossAsset QQQ/SPY 缺失 |
| Gold | Binance PAXG | Yahoo GC=F | Futu GLD | CrossAsset gold 缺失 |
| 跨资产历史 | Futu (长历史) | Yahoo (有限) | — | 部分指标无 q 分位 |

---

## 四、history.db 采集的指标

这些指标没有公开历史 API，guanfu 每次运行写入一行，攒够 30 天后自动回填 q（历史分位）：

| key | 域 | 含义 |
|---|---|---|
| `etf_net_flow_7d_usd` | flow | ETF 7 日净流入 |
| `etf_net_flow_30d_usd` | flow | ETF 30 日净流入 |
| `etf_total_assets_usd` | flow | ETF 总资产 |
| `stablecoin_market_cap_usd` | flow | 稳定币总市值 |
| `stablecoin_supply_30d_pct` | flow | 稳定币 30 日增速（需 30 天采集后计算） |
| `mempool_mb` | network | mempool 拥堵 |
| `hash_rate_ehs` | network | 哈希率 |
| `difficulty_change_pct` | network | 难度调整 % |
| `funding_rate_pct` | positioning | 资金费率 |
| `oi_to_mc` | positioning | OI/市值比 |
| `fear_greed` | positioning | 恐慌贪婪指数 |
| `dxy_60d_trend_pct` | macro | DXY 60 日趋势 |
| `real_yield_10y_pct` | macro | 10Y TIPS |
| `m2_yoy` | macro | M2 同比 |
| `spx_correlation_30d` | macro | BTC/SPX 相关性 |

**注意**：`stablecoin_supply_30d_pct` 需要 history.db 攒够 ≥ 30 天稳定币市值数据才会出现在面板中。

BTC 价格衍生的分位（mayer_multiple、sma_200w_dev、AHR、技术指标等）由 BTC 全历史日线缓存直接计算：CoinMetrics `PriceUSD` 覆盖 2010-07-18 至最新已收盘样本，Binance `BTCUSDT` 覆盖最近日线和当日最新值。它们不进 history.db。

---

## 五、Futu OpenD 部署

### 步骤

1. 从 [Futunn 官网](https://www.futunn.com/download/openAPI) 下载 OpenD 并启动
2. 安装 Python SDK：`pip install futu-api`
3. 将 bridge 脚本放到 `~/.guanfu/futu_bridge.py`
4. 在 OpenD 中登录富途账号（需开户）
5. 设置环境变量（如需自定义地址）：

```bash
export FUTU_GATEWAY="127.0.0.1:11111"
```

### 富途提供的数据

| Futu 代码 | 资产 | 用途 |
|---|---|---|
| US.QQQ | Invesco QQQ Trust | 纳斯达克 100 代理 |
| US.SPY | SPDR S&P 500 ETF | 美股大盘 |
| US.GLD | SPDR Gold Trust | 实物黄金 ETF（约 1/10 oz） |
| US.UUP | Invesco DB USD Index Bullish Fund | DXY 实时代理（比 FRED 更快） |
| US.VIXY | ProShares VIX Short-Term Futures ETF | 恐慌指数代理 |

### 不启动 Futu 时

Yahoo Finance 提供 QQQ、SPY、GC=F 作为降级。部分指标（UUP、VIXY、GLD 长历史、长跨资产相关）缺失，面板对应字段为空。

---

## 六、回测 K 线缓存

回测工具 `guanfu-backtest` 默认复用生产 BTC 日线缓存：CoinMetrics `PriceUSD` 2010-07-18 起全历史 + Binance 最新日线覆盖。这样生产 AHR、实时盘面和回测默认使用同一条 BTC 数据链路。

```bash
guanfu-backtest --all-data --interval 7 --report-md report.md
```

默认缓存路径为 `$CACHE_DIR/btc_daily_history.json`；未设置 `CACHE_DIR` 时使用系统用户缓存目录。可用 `GUANFU_BTC_KLINE_CACHE=/path/to/btc_daily_history.json` 固定路径；`--kline-cache` 可手动指定缓存文件，支持生产缓存 envelope，也兼容旧格式 `{"YYYY-MM-DD": close_price, ...}`。

---

## 七、CoinMetrics 免费端点说明

CoinMetrics 的 community API（无需 key）已收紧。当前 guanfu 使用以下端点。

BTC 日线价格：

```
https://community-api.coinmetrics.io/v4/timeseries/asset-metrics
  ?assets=btc
  &metrics=PriceUSD
```

链上估值：

```
https://community-api.coinmetrics.io/v4/timeseries/asset-metrics
  ?assets=btc
  &metrics=CapMVRVCur,CapMrktCurUSD
```

**限制**：
- 无 realized cap 直接值（CapRealUSD 不在免费 tier）→ MVRV Z-Score 使用 implied realized cap
- 频率限制较严
- 如果端点进一步收紧，MVRV/NUPL 需切换到 Glassnode/CryptoQuant 付费 API

---

## 八、首次部署检查清单

```bash
# 1. 安装
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest

# 2. 可选：注册 FRED API key
export FRED_API_KEY="xxx"  # 否则 Macro 域为空

# 3. 可选：部署 Futu OpenD（跨资产实时数据）
pip install futu-api
mkdir -p ~/.guanfu
cp bin/futu_bridge.py ~/.guanfu/

# 4. 可选：固定 BTC 全历史日线缓存路径（否则默认 $CACHE_DIR/btc_daily_history.json）
export GUANFU_BTC_KLINE_CACHE="$HOME/.guanfu/btc_daily_history.json"

# 5. 首次运行
guanfu  # 创建 ~/.guanfu/history.db，拉取数据 60-90s

# 6. 验证
guanfu --domain valuation  # 检查估值指标是否完整
guanfu --domain macro       # 检查宏观指标（需 FRED_API_KEY）
```

---

## 相关文档

- [README.md](../README.md) — 项目总览
- [SKILL.md (内置于 btc-guanfu skill)](../../../.claude/skills/btc-guanfu/SKILL.md) — 指标手册 + 读盘框架
- [kb/ (知识库)](../../../.claude/skills/btc-guanfu/kb/) — 8 个因果推理文件
- [AHR999 回测报告](./backtest-baseline-ahr999-*.md)
