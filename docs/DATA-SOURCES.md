# 数据源与配置 (guanfu v2)

> 26 个数据集，覆盖 5 资产 × 5 维度。全部存储在 `~/.guanfu/prices/` JSON 日频存档。

---

## 一、数据源全景

### 免费数据源（开箱即用）

| 数据源 | 数据集 | 资产 | 频率 | 起点 | 费用 |
|--------|--------|------|------|------|------|
| CoinMetrics | btc, btc_mvrv, btc_txcnt, btc_hashrate | BTC | 日 | 2010-07 | 免费 |
| Yahoo Finance | qqq, spy, gold(GC=F) | QQQ/SPY/Gold | 日 | ~2015 | 免费 |
| AkShare | hs300, hs300_volume | CSI300 | 日 | 2002-01 | 免费 |
| AkShare | hs300_pmi, hs300_m2, hs300_cpi, hs300_retail | CSI300 | 月 | 2008-01 | 免费 |
| AkShare | hs300_lpr | CSI300 | 日 | 1991-04 | 免费 |
| AkShare | hs300_northbound | CSI300 | 日 | 2014-11 | 免费 |
| AkShare | hs300_cny | CSI300 | 日 | 1994-01 | 免费 |
| CFTC | gold_cot (COT投机持仓) | Gold | 周 | 1986-01 | 免费 |
| Shiller | spx_cape (CAPE) | SPY/QQQ | 月 | 1871-01 | 免费 |

### 需注册 API Key

| 数据源 | 数据集 | 频率 | 注册 | 环境变量 |
|--------|--------|------|------|---------|
| FRED | fred_dfii10, fred_dgs10, fred_dxy, fred_yield_curve, fred_breakeven, fred_hy_spread | 日/月 | fred.stlouisfed.org | `FRED_API_KEY` |

### 本地网关

| 数据源 | 用途 | 部署 |
|--------|------|------|
| Futu OpenD | QQQ/SPY K线(3000d+)、PE/PB快照、SH.000300 | `FUTU_GATEWAY=127.0.0.1:11111` |

---

## 二、数据管道

```
首次运行: 全量导入 → ~/.guanfu/prices/{asset}.json
后续运行: 增量更新 → 仅拉取 last_date 之后的数据
诊断: guanfu status → 查看所有数据集状态
```

---

## 三、环境变量

```bash
FRED_API_KEY=xxx          # FRED 宏观数据
FUTU_GATEWAY=127.0.0.1:11111  # Futu OpenD 地址
GUANFU_BTC_KLINE_CACHE=~/cache/btc.json  # BTC K线缓存
GUANFU_NO_HISTORY=1       # 禁用 history.db
GUANFU_SKILL_PATH=./skill/SKILL.md  # MCP server SKILL路径
```

---

## 四、降级路径

| 场景 | 降级策略 |
|------|---------|
| FRED 不可用 | TLT 替代实际利率, UUP 替代 DXY |
| Futu 不可用 | Yahoo 替代 QQQ/SPY |
| CoinMetrics 不可用 | Binance 1000d 替代 |
| AkShare 不可用 | Yahoo 000300.SS 替代 (2021+) |
