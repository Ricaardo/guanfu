# 观复 v2 多资产完整改造方案

```text
P0 ▸ 显示/逻辑 bug         P2 ▸ 预测精度提升
P1 ▸ 功能/数据缺失         P3 ▸ 架构扩展/调研
```

---

## Track A — 读盘修复（6 项）

| 优先级 | # | 问题 | 根因 | 修法 | 依赖 | 文件 |
|---|---|---|---|---|---|---|
| **P0** | A1 | Gold/HS300 价格显示 $0.00 | `printEquityPanel` 硬编码 `SPYPrice+QQQPrice`；Gold 覆盖 Snapshot 后两字段归零 | 根据 asset 类型选 `GoldPrice`/`SPYPrice`/`QQQPrice`/`Price` | — | `cmd/guanfu/main.go` |
| **P0** | A2 | sma_200_dev 显示 100x | `formatValue` 行 727 `v*100` 双倍算 | ✅ 已修 | — | — |
| **P1** | A3 | Gold 读盘缺 DXY | `uup.json` 不存在于 PriceStore → FetchSnapshot 读到 Close=0 | 拉 FRED `DTWEXBGS` 或 Yahoo UUP → 存 uup.json；修 gold panel DXY label | — | `scripts/` + `asset_gold.go` |
| **P1** | A4 | HS300 读盘不显示 PMI/M2/LPR | `BuildPanel` 只写了 TLT proxy 和 CNY/USD，没接入 HS300 专属数据 | 在 BuildPanel 中从 PriceStore 读取中国宏观数据 → 写入 `panel.Macro` | — | `asset_hs300.go` |
| **P2** | A5 | QQQ/SPY 读盘缺 VIX context | EquityExtractors 有 VIX 但 panel 显示没有 | `asset_qqq.go`/`spy.go` BuildPanel 加 VIX 到 cross_asset domain | — | `asset_qqq.go`, `asset_spy.go` |
| **P3** | A6 | 任意 stock 读盘 | `readStock` 只有 forecast，无 panel | 复用 `BuildEquityPanel` 做技术面板 | 等 D3 稳定后 | `main.go` |

---

## Track B — 预测特征调优（6 项）

| 优先级 | # | 改动 | 当前 | 目标 | 回测预期 | 依赖 |
|---|---|---|---|---|---|---|
| **P1** | B1 | Gold: DXY weight 0.45 → ≥0.7 | DXY 与 RSI 同权重 | DXY 是 gold 最主导变量 | 180d PIT 0.68→0.60 | — |
| **P1** | B2 | QQQ/SPY: CAPE weight 0.55 → ≥0.8 | CAPE 与 drawdown 同权重 | 长端 CAPE 最重要 | 180d PIT 0.59→0.55 | — |
| **P1** | B3 | EquityExtractors 加 VIX | 只有 Gold 有 VIX | 美股也需要风险偏好区分 | 30d dir +2~5% | — |
| **P3** | B4 | HS300 日频特征调研 | PMI/M2/LPR 月频→kNN 近邻实质是"时间近" | 先验证 `hs300_volume` 方向命中率；可行再扩到北向/波动率/期货贴水 | 不确定，先验证 | — |
| **P2** | B5 | Per-asset horizon | 全部 30/90/180 | QQQ 加 63d(季度)/252d(年度)；Gold 加 60/120 | PIT 校准改善 | — |
| **P3** | B6 | LearnWeights RFC | `Option.LearnWeights` 存在但 `Build()` 不检查 | 需 RFC 明确技术选型（线性回归系数? 互信息?）后再落地 | 不确定性大 | — |

### B4 说明

HS300 日频特征（volume、波动率等）需要先验证有效性再做——跑一个简化 backtest 只带 volume 特征看方向命中率是否优于随机。**如果 volume 无效则整个 B4 放弃**，接受 HS300 45% dir hit 为天花板。

### B6 RFC 待定项

- 学习方法：线性回归 coefficient / 随机森林 importance / 互信息
- 训练窗口：全局 vs 滚动
- 特征缺失处理
- 回测验证方法

---

## Track C — AI 通路（6 项）

| 优先级 | # | 改动 | 现状 | 目标 | 依赖 | 文件 |
|---|---|---|---|---|---|---|
| **P0** | C1 | MCP 工具加别名 + 重描述 | 工具名 `get_btc_panel` 误导 AI 认为仅支持 BTC | dispatch 加别名 `get_panel`/`get_btc_panel`（向后兼容）；描述明确标注支持 `asset=qqq/spy/gold/hs300/btc` | — | `cmd/guanfu-mcp/main.go` |
| **P1** | C2 | MCP 新增 get_stock_forecast | 没有任意 stock 入口 | `get_stock_forecast(ticker, horizons)` → Yahoo fetch + kNN forecast | D1+D2+D3 | `cmd/guanfu-mcp/main.go` |
| **P1** | C3 | resource/latest 支持资产参数 | `panel/latest` 硬编码 btc | 加 `panel/latest/qqq`/`spy`/`gold`/`hs300` URI | C1 后 | `cmd/guanfu-mcp/main.go` |
| **P1** | C4 | SKILL.md 重写 | 只有 CLI 用法，无 MCP 工具节 | 拆 3 节：CLI 命令、MCP 工具表（输入/输出/示例）、kNN 原理 | — | `skill/SKILL.md` |
| **P2** | C5 | 新建 CLAUDE.md | 不存在 | 项目架构、文件映射、AI 助手决策参考 | — | `CLAUDE.md` |
| **P3** | C6 | JSON output 标准化 | keys 大小写不统一 (`technical` vs `Technical`) | 全走小写 snake_case | — | `model/panel.go` + CLI |

### C1 实现方案

```go
// dispatch — 新旧名字共存
case "get_panel", "get_btc_panel":
    // ... 同一 handler
case "get_verdict", "get_btc_verdict":
    // ... 同一 handler
case "get_forecast", "get_btc_forecast":
    // ... 同一 handler
```

**不删除旧名**，至少保留一个版本。AI 可以任选一个名字调用。

---

## Track D — 任意美股推广（4 项）

| 优先级 | # | 改动 | 说明 | 依赖 | 文件 |
|---|---|---|---|---|---|
| **P2** | D1 | Yahoo auto-fetch + TTL 缓存 | `FetchAndCacheStock(ticker, days)` → 复用 `fetchYahooChart` → PriceStore Save + TTL 检查（缓存≤1d 直接返回，否则增量 fetch） | — | `pkg/client/fetch_stock.go` (新建) |
| **P2** | D2 | USStockExtractors | `GenericTechnical + DGS10 + DXY + HY spread + yield curve + VIX` (13 feat, 无 CAPE) | — | `bundles.go` |
| **P2** | D3 | readStock 赋能 | 先查 PriceStore, 无→auto fetch→forecast；切到 `USStockExtractors` | D1+D2 | `main.go` |
| **P2** | D4 | import-stock CLI | `guanfu import-stock AAPL` → 手动 trigger Yahoo 全量拉取 | D1 | `main.go` |

### D1 TTL 逻辑

```text
FetchAndCacheStock(ticker, days):
  1. PriceStore.Load(ticker)
  2. 有数据且最新日期 ≤1d ago → 返回
  3. 有数据但过期 → 增量 fetch 缺失天数 → PriceStore.Append
  4. 无数据 → Yahoo chart 全量 fetch (days) → PriceStore.Save
```

---

## 执行路径

### 第 0 波 — AI 通路解锁 + 低成本修复

```
C1   ▸ MCP 工具加别名 + 重描述         [~5 行 dispatch, 最高 ROI]
A1   ▸ Gold/HS300 价格显示修复          [+3 行]
A4   ▸ HS300 panel 补 PMI/M2          [+15 行]
B1   ▸ Gold DXY weight 0.45→0.7       [1 行]
B3   ▸ EquityExtractors 加 VIX         [+1 行 bundles.go]
B2   ▸ CAPE weight 0.55→0.8            [1 行]
     ↳ go test ./pkg/engine -run TestBacktestBundles -v
C4   ▸ SKILL.md 重写                    [文档]
```

### 第 1 波 — 任意美股 + 数据补全

```
D1   ▸ Yahoo auto-fetch + TTL          [新建 fetch_stock.go, ~60 行]
D2   ▸ USStockExtractors               [+10 行 bundles.go]
D3   ▸ readStock 升级 + auto-fetch     [~20 行 main.go]
C2   ▸ get_stock_forecast MCP          [+~30 行 mcp/main.go]
C3   ▸ resource/latest 参数化          [+~15 行 mcp/main.go]
A3   ▸ Gold DXY 补 UUP data            [新建 fetch 脚本]
A5   ▸ QQQ/SPY panel 加 VIX            [+5 行]
```

### 第 2 波 — 精度 + 文档

```
B5   ▸ Per-asset horizon               [~10 行]
C5   ▸ CLAUDE.md                        [文档]
```

### 待定（需额外调研后决定是否做）

```
B4   ▸ HS300 日频特征                   [先验证 volume 有效性]
B6   ▸ LearnWeights                     [需 RFC 明确技术选型]
C6   ▸ JSON 标准化                      [破坏性变更，等重]
A6   ▸ stock panel                      [等 D3 稳定后]
```

---

## 验证方法

每次改动后运行基准 backtest 验证回归：

```bash
go test ./pkg/engine -run TestBacktestBundles -v
```

预期基准线：

| 资产 | 30d dir | 90d dir | 180d dir | PIT(30d) |
|---|---|---|---|---|
| BTC | 54% | 65% | 65% | 0.48 |
| QQQ | 70% | 70% | 80% | 0.48 |
| SPY | 70% | 75% | 80% | 0.55 |
| Gold | 50% | 65% | 55% | 0.53 |
| HS300 | 45% | 47% | 49% | 0.52 |

---

## 文件索引

| 文件 | 改动 | 波次 |
|---|---|---|
| `cmd/guanfu/main.go` | printEquityPanel price 显示, readStock 升级, import-stock CLI | 0, 1 |
| `cmd/guanfu-mcp/main.go` | 工具别名 + 重描述, resource 参数化, get_stock_forecast | 0, 1 |
| `pkg/engine/asset_gold.go` | BuildPanel DXY 修复, 读盘显示 | 1 |
| `pkg/engine/asset_hs300.go` | BuildPanel PMI/M2/LPR 补全 | 0 |
| `pkg/engine/asset_qqq.go` | BuildPanel VIX 添加 | 1 |
| `pkg/engine/asset_spy.go` | BuildPanel VIX 添加 | 1 |
| `pkg/forecast/features/enhanced.go` | DXY weight, CAPE weight, (已有 VIX/hy_spread) | 0 |
| `pkg/forecast/features/bundles.go` | EquityExtractors 加 VIX, USStockExtractors | 0, 1 |
| `pkg/client/fetch_stock.go` | Yahoo auto-fetch + TTL (新建) | 1 |
| `skill/SKILL.md` | MCP 工具节 + CLI 更新 | 0 |
| `CLAUDE.md` | 项目上下文 (新建) | 2 |
| `scripts/import_cape.py` | CAPE ingestion | — |

---

## 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-05-08 | 初版，4 Track x 22 项 |
| v2 | 2026-05-08 | 加 Dependency 列；C1 加别名不改名；B4 降 P3 + 加验证说明；B6 摘出需 RFC；D1 加 TTL；执行顺序重排为 0/1/2/待定 四波 |
