# 数据源与配置 (guanfu v3)

> 覆盖 4 核心资产 + 任意美股 + 宏观 + 情绪。全部 JSON 日频存档在 `~/.guanfu/prices/`。统一 `guanfu refresh` 刷新,首次全量 / 后续增量。

---

## 一、统一刷新框架 (`guanfu refresh`)

Source 实现 `client.Source` 接口 (`Key / DisplayName / Refresh`),按 dependency order 串行执行。

```bash
guanfu refresh              # 全部刷新
guanfu refresh --dry-run    # 列出将要刷新的 source,不真正拉数据
guanfu refresh --only=btc,fred_dxy    # 只刷指定 key(逗号分隔)
guanfu refresh --skip=gold_cot        # 跳过指定 key
```

**增量协议** (`staleThreshold`):
- `last_date` ≤ 24h → `mode="skip"`,不发请求
- `last_date` 缺失 / > 24h → fetch `lastDate+1 → today`(FRED / Yahoo)
- CAPE 月频 → 28 天 TTL
- Stock 30h TTL(覆盖周末 gap)

**失败容忍**:单个 source 失败不影响其他;`fail` 状态在最终表中 surface,CLI exit code 1,但 stdout 保留所有结果。

---

## 二、数据源清单(按刷新顺序)

### 核心价格(2)

| Key | 资产 | 源 | 频率 | 起点 | 费用 |
|---|---|---|---|---|---|
| `btc` | BTC | CoinMetrics `PriceUSD` + Binance 增量 | 日 | 2010-07-18 | 免费 |
| `gold` | 黄金 | Yahoo `GC=F`(WTI futures,前身是 `XAUUSD=X`,2025 退市后切换) | 日 | 2000-08-30 | 免费 |

### Yahoo ETF / 指数(6)

| Key | 标的 | 用途 |
|---|---|---|
| `qqq` | QQQ | Nasdaq-100 ETF |
| `spy` | SPY | S&P 500 ETF |
| `vixy` | ^VIX | 波动率指数 |
| `tlt` | TLT | 20Y+ Treasury ETF |
| `uup` | UUP | USD Bullish ETF(DXY 实时代理) |
| `usd_cny` | CNY=X | USD/CNY 汇率 |

### FRED 宏观(6,需 `FRED_API_KEY`)

| Key | Series | 用途 | 起点 |
|---|---|---|---|
| `fred_dxy` | `DTWEXBGS` | Trade-weighted USD (Broad) | 2006-01-01 |
| `fred_dgs10` | `DGS10` | 10Y Treasury yield | 1990-01-01 |
| `fred_dfii10` | `DFII10` | 10Y TIPS / real yield | 2003-01-01 |
| `fred_yield_curve` | `T10Y2Y` | 10Y-2Y spread | 1976-06-01 |
| `fred_breakeven` | `T10YIE` | 10Y breakeven inflation | 2003-01-01 |
| `fred_hy_spread` | `BAMLH0A0HYM2` | BofA US HY OAS | 1996-12-31 |

FRED 注册: <https://fred.stlouisfed.org>(免费,个人使用无限额)。

### 估值(1,Python script)

| Key | 源 | 频率 | 起点 |
|---|---|---|---|
| `spx_cape` | Shiller Online Data XLS | 月 | 1871-01 |

CAPE import: `scripts/import_cape.py`(一次性拉 XLS 转 JSON,28d TTL)。

### 任意美股(动态,`stock_*`)

通过 `guanfu import-stock TICKER [DAYS]` 或 `guanfu stock TICKER` 自动触发:

```bash
guanfu import-stock AAPL 3650   # 手动全量拉 10 年
guanfu stock MSFT               # 首次使用时自动 fetch 10 年
```

命名空间:`stock_<lowercase_ticker>.json`(如 `stock_aapl.json`)。严格校验 — 不允许与核心资产 key 冲突。30h TTL(覆盖周末)。

---

## 三、BTC 专用源(未进 refresh 框架,独立拉取)

这些源在 BTC panel build 时即时拉取,不走 `guanfu refresh`,走各自的 TTL 缓存:

| 数据 | 源 | 用途 |
|---|---|---|
| BTC ETF 净流入 | [SoSoValue](https://sosovalue.com) API | 7d / 30d 净流入 |
| 总市值 / BTC 市占率 / 稳定币市值 | [CoinGecko](https://www.coingecko.com) | Flow / Market share |
| 恐慌贪婪指数 | [alternative.me](https://alternative.me/crypto/fear-and-greed-index/) | Positioning |
| 哈希率 / 难度 / mempool | [mempool.space](https://mempool.space) | Network |
| ETH/Top50 kline / 资金费率 / OI | Binance | Positioning + 山寨季 |
| MVRV / NUPL | CoinMetrics community | Valuation(付费 tier 需 `COINMETRICS_API_KEY`) |

**注**:这些源未统一 refresh 是技术债(v3 Track I4),但它们的 TTL 机制和 refresh 框架功能等价,用户无感。

---

## 四、本地网关(可选)

### Futu OpenD — 实时 ETF 报价

Go 端无法直连 OpenD(需加密握手),通过 Python bridge 代理:

```bash
pip install futu-api
mkdir -p ~/.guanfu
curl -sL https://raw.githubusercontent.com/Ricaardo/guanfu/main/pkg/client/futu_bridge.py \
  -o ~/.guanfu/futu_bridge.py
```

用途: QQQ / SPY / GLD / UUP / VIXY / USO (oil proxy) 实时报价,Yahoo 降级 fallback。

| 环境变量 | 默认 | 作用 |
|---|---|---|
| `FUTU_GATEWAY` | `127.0.0.1:11111` | OpenD 地址 |
| `FUTU_ENABLED=0` | 未设置 | 禁用 Futu,直接走 Yahoo |
| `FUTU_BRIDGE` | (自动探测) | 自定义 bridge 路径 |

探测顺序:`$FUTU_BRIDGE` → 二进制同目录 → `~/.guanfu/` → `~/.config/guanfu/`。

---

## 五、环境变量速查

完整列表（~20 个变量，含默认值 + 说明）见 [`deployment.md`](deployment.md) § 环境变量速查。与数据源直接相关的核心变量：

| 变量 | 作用 | 影响 |
|---|---|---|
| `FRED_API_KEY` | FRED 宏观数据 | 缺失时 Macro 域为 placeholder |
| `COINMETRICS_API_KEY` | MVRV / NUPL 付费 tier | 缺失时 Valuation 缺 MVRV Z |
| `GUANFU_NO_HISTORY=1` | 禁用 history.db | 跳过 15 个非价格指标的 q 分位 |

---

## 六、降级路径

| 首选源不可用 | 降级到 |
|---|---|
| FRED 不可用 | TLT 替代实际利率;UUP 替代 DXY |
| Futu 不可用 | Yahoo 替代 QQQ / SPY / UUP |
| CoinMetrics 付费不可用 | 社区 tier 最多拿到价格,MVRV / NUPL 变 missing |
| Yahoo `XAUUSD=X` 退市 | Yahoo `GC=F`(已切换,历史扩到 2000+) |

**`source_health` 面板** — 每个 source 显式带 ok / partial / stale / missing / warning 状态,`as_of` 时间,`fallback_used` 标记,`note`。读盘前先查 `source_health`,避免用 stale / fallback 数据做强结论。

---

## 七、history.db(SQLite)— 非价格指标历史分位

ETF 净流入 / mempool / 资金费率 / 宏观这类**没有公开历史 API** 的指标,guanfu 通过 `~/.guanfu/history.db` 每天采集一行,攒够样本后回填 `q` 字段。

**采集的 15 个指标**:
- Flow: `etf_net_flow_7d_usd`, `etf_net_flow_30d_usd`, `etf_total_assets_usd`, `stablecoin_market_cap_usd`, `stablecoin_supply_30d_pct`
- Network: `mempool_mb`, `hash_rate_ehs`, `difficulty_change_pct`
- Positioning: `funding_rate_pct`, `oi_to_mc`, `fear_greed`
- Macro: `dxy_60d_trend_pct`, `real_yield_10y_pct`, `m2_yoy`, `spx_correlation_30d`

**时效**:
- 第 1 天:入库,无 q
- 第 30 天:开始显示 q
- 第 365 天:q 完全有意义
- 第 730 天:回看窗口上限,老数据滚动淘汰

BTC 价格相关指标(`sma_200w_dev` / `mayer_multiple` / AHR / 技术指标)的 q 由 BTC 全历史日线直接算,**不进** history.db。

---

## 八、数据目录结构

```
~/.guanfu/
├── prices/                       # JSON 日频存档(refresh 输出)
│   ├── btc.json / qqq.json / spy.json / gold.json
│   ├── btc_mvrv.json / btc_txcnt.json / btc_hashrate.json
│   ├── fred_dxy.json / fred_dgs10.json / fred_dfii10.json / ...
│   ├── spx_cape.json
│   ├── gold_cot.json
│   ├── vixy.json / tlt.json / uup.json / usd_cny.json
│   └── stock_aapl.json / stock_msft.json / ...    # 任意美股
├── history.db                     # SQLite:15 非价格指标历史分位(730d 滚动)
├── panels/                        # (可选)每日盘面 archive,供 guanfu-similar 使用
│   └── YYYY-MM-DD.json
├── cache/                         # guanfu-backtest / 开发工具缓存
├── claims/                        # (规划中 v3 Track K)Claim + Intent ledger
│   └── YYYY-MM/YYYY-MM-DD-{asset}-{horizon}.json
├── alerts/                        # (规划中 v3 Track L)watch 触发记录
└── portfolio.yaml                 # (规划中 v3 Track L, opt-in)组合上下文
```

---

## 九、新增数据源的 checklist

1. 实现 `client.Source` 接口:`Key / DisplayName / Refresh(ctx, store)`
   - 在已有 `*_history.go` 里加(如新增 FRED series),或新建一个文件(如新源家族)
2. 在 `cmd/guanfu/cli_refresh.go: allRefreshSources()` 注册
3. 跑 `go test ./pkg/client/ -run TestRefresh` 验证
4. 跑 `guanfu refresh --only=<new_key> --dry-run` 确认列表
5. 跑真实 refresh,验证 `~/.guanfu/prices/<new_key>.json` 写入正确
6. 如果新源驱动某个 forecast feature,在 `pkg/forecast/features/bundles.go` 的对应 `XExtractors` 函数里加 extractor,**数据缺失时返回 `nil, false`**(bundles 会自动 skip)
7. 跑 `TestBacktestBundles` 确认回归预算内(见 `docs/archive/v3/guanfu-v3-todo.md` 回归预算表)

---

## 十、变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2025-Q4 | 初版 26 数据集 |
| v2 | 2026-05-09 | 统一 refresh 框架 (23 Source) + 任意美股 (`stock_*`) + Gold 切 GC=F |
| **v3** | **2026-05-09** | **对齐 v3 roadmap**:30+ 数据集,新 BTC ad-hoc 源独立 section,`source_health` 强化,数据目录结构明示 Track K/L 未来扩展点 |
