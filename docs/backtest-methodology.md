# 观复历史相似度与回测方法

推广材料里的历史收益或“相似时刻”必须从可复现流程生成，不能手写挑样本。

## 面板归档

每天保存一次完整 JSON 面板（`~/.guanfu/panels/` 是 `guanfu-similar` 的默认 `--history-dir`）：

```bash
mkdir -p ~/.guanfu/panels
guanfu --json > "$HOME/.guanfu/panels/$(date -u +%F).json"
```

挂在 cron / launchd 上每天跑一次：

```cron
0 9 * * * /usr/bin/env -S bash -lc 'guanfu --json > ~/.guanfu/panels/$(date -u +%F).json 2>> ~/.guanfu/cron.log'
```

如果要让 ETF、funding、mempool、宏观等指标带历史分位，**不要**设置 `GUANFU_NO_HISTORY=1`，并长期保留同一个 `~/.guanfu/history.db`。

## 相似度计算

`guanfu-similar` 只比较双方都有 `q` 分位的指标。每个共享指标用 q 分位差计算欧氏距离：

```text
distance = sqrt(mean((current_q - history_q)^2))
similarity = max(0, 1 - distance) * 100
```

这种做法避免把美元、市值、利率、相关系数等不同量纲强行混在一起。缺点是早期样本如果没有足够 history.db，会因为共享 q 指标太少而被跳过。

## 命令

```bash
# 默认从 ~/.guanfu/panels 读 archive
guanfu --json | guanfu-similar --top 8

# 显式路径
guanfu --json > current.json
guanfu-similar --current current.json --history-dir /path/to/panels --top 8
```

输出示例：

```markdown
| rank | date | similarity | matched_q | distance | file |
|---:|---|---:|---:|---:|---|
| 1 | 2024-08-05 | 87.2% | 28 | 0.1280 | /Users/x/.guanfu/panels/2024-08-05.json |
```

## 收益统计口径

任何 `+30%`、`+100%` 这类宣传数字都必须同时标注：

- 样本日期来自 `guanfu-similar` 输出，不人工挑选。
- 价格源与面板价格源一致，默认使用 Binance BTCUSDT 日线收盘价。
- forward return 窗口固定，例如 30d / 90d / 180d / 365d，不能事后改窗口。
- 样本数量、`matched_q` 下限、相似度下限需要写清楚。
- 同时披露反例和最差 forward return。

在自动 forward-return 报告补齐前，公开推广材料只展示相似度方法，不展示历史收益数字。

---

## Walk-forward 矩阵(v3 新增,pkg/engine/backtest_bundles_test.go)

单一汇总命中率会**掩盖 regime 依赖**。`TestBacktestBundles` 按 (year, horizon) 切分,输出可以直接看出哪些年份的样本在拖/拉高平均。

### 典型输出格式

```
btc 180d walk-forward:
  2015:  dir_hit 0.71  n=4
  2016:  dir_hit 0.67  n=6
  2017:  dir_hit 0.83  n=6
  2018:  dir_hit 0.25  n=4       ← regime 不匹配:熊市 kNN 在牛市训练集找不到邻居
  2019:  dir_hit 0.60  n=5
  2020:  dir_hit 1.00  n=3
  ...
```

### 如何读这个矩阵

| 模式 | 含义 | 行动 |
|---|---|---|
| 各年均衡 50-70% | 信号在全 regime 可用 | 正常使用 |
| 少数年份极端低 (<30%) | 某个 regime(通常是 2018/2022 风险去杠杆)失效 | 在类似 regime 到来时降级,或加 regime gating (Track G2) |
| 多数年份都低 (<50%) | 全 regime 弱信号 | **hard-block**(如 HS300 全 horizon) |
| 前 N 年好、后 N 年差 | 结构性失效,特征老化 | 可能需 recency-weighted kNN (Track G5) 或删特征 |

### 当前观察(2026-05-09 post-refresh,v6 baseline)

纯 (asset, horizon) 命中率表见 [`skill/tier1.md`](../skill/tier1.md) § 3。本表聚焦 walk-forward **诊断**：

| 资产 | 全局 dir_hit | walk-forward 诊断 |
|---|---|---|
| BTC 90d | 65% | 各年均衡 55-80%,无 regime 偏斜 |
| QQQ 180d | 80% | 高峰在 2021-2023 (~90%),2018 低至 50% 但样本少;整体稳 |
| SPY 180d | 85% | 2018 例外,其余年份 ≥75%;SPY 比 QQQ 更低波动,walk-forward 更稳 |
| Gold 90d | 55% | **强 regime 依赖** — 2017-2022 ≤50%,2023-2025 50-100%,央行购金 regime 改变了主导变量 |
| Gold 180d | 49% | 同上但更严重 → hard-block |
| HS300 全 horizon | 45-49% | **全 regime 弱**,无论牛熊年份都接近随机 → hard-block |

### 如何生成最新矩阵

```bash
go test ./pkg/engine/ -run TestBacktestBundles -v
```

需要 `~/.guanfu/prices/` 有完整数据(`guanfu refresh` 先跑);CI 下无数据时 skip。

### 更新 reliability 表

如果 walk-forward 数字变化超出回归预算(任一 horizon dir_hit 下降 ≥ 3pp),更新 `pkg/forecast/reliability.go` 的 `assetHorizonReliability` 表 + `AsOf` 字段。具体规则见 `docs/archive/v3/guanfu-v3-todo.md` 回归预算节。

### 为什么不在 BuildForecast 里实时跑 walk-forward

- IO 重:需要 PriceStore 所有历史 + 重算每个特征
- 慢:5 资产 × 多 horizon × 多年 = 数百次 kNN,一次 panel 请求延迟飙到分钟级
- 不必要:walk-forward 变化是月/季度尺度,没必要每次请求重算

所以是**离线跑、写回 reliability 表、Build 时 lookup**。
