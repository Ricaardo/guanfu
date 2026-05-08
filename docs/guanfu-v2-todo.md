# 观复 v2 多资产完整改造方案

```text
P0 ▸ 显示/逻辑 bug         P2 ▸ 预测精度提升
P1 ▸ 功能/数据缺失         P3 ▸ 架构扩展/调研
```

---

## Track A — 读盘修复（7 项）

| 优先级 | # | 问题 | 根因 | 修法 | 依赖 | 文件 |
|---|---|---|---|---|---|---|
| **P0** | A0 | `SnapshotData` 没有 `HS300Price` 字段，且 panel 没有 top-level `Price` | A1 修法依赖此字段，否则只能用 kludge 复用 QQQPrice | 在 `pkg/model/types.go` 加 `HS300Price float64`（或更通用的顶层 `Price`），bump `CurrentMarketSnapshotSchemaVersion` | — | `pkg/model/types.go` |
| **P0** | A1 | Gold/HS300 价格显示 $0.00 | `printEquityPanel` 行 427/429 硬编码 `SPYPrice+QQQPrice`；Gold 写入 `GoldPrice`，HS300 完全不写 price | 按 `panel.Asset` / 资产 key 选字段：QQQ→QQQPrice、SPY→SPYPrice、Gold→GoldPrice、HS300→HS300Price（A0 加） | A0 | `cmd/guanfu/main.go` |
| **P0** | A2 | sma_200_dev 显示 100x | `formatValue` 行 727 `v*100` 双倍算 | 修复在 working tree（未 commit），需独立 commit 让历史可追溯 | — | `cmd/guanfu/main.go` |
| **P1** | A3 | Gold 读盘缺 DXY label/value | DXY 数据已存在于 `~/.guanfu/prices/fred_dxy.json`，panel 端没有读 | `asset_gold.go` BuildPanel 用 `store.Latest("fred_dxy")` 写入 `panel.CrossAsset["dxy"]` —— **无需新拉数据** | — | `asset_gold.go` |
| **P1** | A4 | HS300 读盘不显示 PMI/M2/LPR | `BuildHS300Dashboard` 只写 `cny_usd`（plan v1 误称还有 "TLT proxy"）；中国宏观数据 PMI/M2/LPR/Northbound 已存在 PriceStore (`hs300_pmi.json` 等) | `BuildPanel` 通过 PriceStore 读出 PMI/M2/LPR/Northbound 最新值 → 写入 `panel.Macro` —— **无需新拉数据** | — | `asset_hs300.go`, `hs300_dashboard.go`（dashboard 输入需扩 PriceStore 句柄或新字段） |
| **P2** | A5 | QQQ/SPY 读盘缺 VIX context | EquityExtractors 有/将有 VIX，但 panel 显示没有 | 先确认 Snapshot.VIXYPrice 已被 FetchSnapshot 填充；再 `asset_qqq.go`/`spy.go` BuildPanel 加 VIX 到 `panel.CrossAsset` | B3 落地（如同 wave）+ FetchSnapshot 已写 VIXY | `asset_qqq.go`, `asset_spy.go` |
| **P3** | A6 | 任意 stock 读盘 | `readStock` 只有 forecast，无 panel | 复用 `BuildEquityPanel` 做技术面板（`USStockExtractors` 跑出 indicators） | **D2**（USStockExtractors）+ D3（readStock 改造）—— 建议直接合入 D3，避免独立 wave | `main.go` |

---

## Track B — 预测特征调优（6 项）

| 优先级 | # | 改动 | 当前 | 目标 | 回测预期 | 依赖 |
|---|---|---|---|---|---|---|
| **P1** | B1 | Gold: DXY weight 0.45 → ≥0.7 | DXY 与 RSI 同权重 | DXY 是 gold 最主导变量 | 180d PIT 0.68 → 0.60（**注**：PIT 目标 ≈ 0.5，"下降" = 更校准，不是变差） | 权重预算（见 § 权重总和约束） |
| **P1** | B2 | QQQ/SPY: CAPE weight 0.55 → ≥0.8 | CAPE 与 drawdown 同权重 | 长端 CAPE 最重要 | 180d PIT 0.59 → 0.55（同上） | 权重预算 |
| **P1** | B3 | EquityExtractors 加 VIX | 只有 Gold 有 VIX | 美股也需要风险偏好区分 | 30d dir +2~5% | 权重预算 + FetchSnapshot 已写 VIXY |
| **P3** | B4 | HS300 日频特征调研 | PMI/M2/LPR 月频→kNN 近邻实质是"时间近" | 先验证 `hs300_volume` 方向命中率；可行再扩到北向/波动率/期货贴水 | 不确定，先验证 | — |
| **P2** | B5 | Per-asset horizon | 全部 30/90/180 | QQQ 加 63d(季度)/252d(年度)；Gold 加 60/120 | PIT 校准改善 | 见 B5 实现注 |
| **P3** | B6 | LearnWeights RFC | `Option.LearnWeights` 存在但 `Build()` 不检查 | 需 RFC 明确技术选型（线性回归系数? 互信息?）后再落地 | 不确定性大 | — |

### B5 实现注

`BuildForecast` 链路里有 `if len(opts.Horizons) == 0 { opts = forecast.DefaultOptions() }`，会把传入的 horizons 清空时回落到 30/90/180。Per-asset horizon 落地时需要在 asset 层显式构造 `forecast.Options{Horizons: ...}` 并 **不能** 走 DefaultOptions 路径，否则被覆盖。

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
| **P0** | C1 | MCP 工具加别名 + 重描述 + **alias 单测** | 工具名 `get_btc_panel` 误导 AI 认为仅支持 BTC；测试只覆盖 `get_btc_*` | dispatch 加别名 `get_panel`/`get_btc_panel`（向后兼容）；描述明确标注支持 `asset=qqq/spy/gold/hs300/btc`；`main_test.go` 加 `get_panel/get_verdict/get_forecast` 等价 case | — | `cmd/guanfu-mcp/main.go` + `main_test.go` |
| **P1** | C2 | MCP 新增 get_stock_forecast | 没有任意 stock 入口 | `get_stock_forecast(ticker, horizons)` → Yahoo fetch + kNN forecast | D1+D2+D3 | `cmd/guanfu-mcp/main.go` |
| **P1** | C3 | resource/latest 支持资产参数 + list resources 列举 | `panel/latest` 硬编码 btc | 加 `panel/latest/qqq`/`spy`/`gold`/`hs300` URI；`resources/list` 返回所有可用 asset 资源 | C1 后 | `cmd/guanfu-mcp/main.go` |
| **P1** | C4 | SKILL.md 重写（**与 C1 同 commit**） | 只有 CLI 用法，无 MCP 工具节 | 拆 3 节：CLI 命令、MCP 工具表（输入/输出/示例）、kNN 原理 | C1 | `skill/SKILL.md` |
| **P2** | C5 | 新建 CLAUDE.md | 不存在 | 项目架构、文件映射、AI 助手决策参考；面向 **维护者/AI coder**（与 C4 面向 skill 消费方不同） | D 系列落地后写更有信息量 | `CLAUDE.md` |
| **P3** | C6 | JSON output 标准化 | keys 大小写不统一 (`technical` vs `Technical`) | **先列消费方清单**（MCP 客户端、SKILL skill、外部脚本、`reports/` 输出），然后才能评估破坏性范围；不列清单不动手 | 消费方清单 | `model/panel.go` + CLI |

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

### C1 单测要求

`main_test.go` 现仅测 `get_btc_panel/verdict/forecast`。alias 加完后必须加：

```go
for _, name := range []string{"get_panel", "get_verdict", "get_forecast"} {
    out, rpcErr := handleToolCall(name, json.RawMessage(`{}`))
    if rpcErr != nil { t.Fatalf(...) }
    // 至少 assert 输出非空、含 panel/verdict/forecast key
}
```

否则 future refactor 误删 alias 不会被捕获。

---

## Track D — 任意美股推广（4 项）

| 优先级 | # | 改动 | 说明 | 依赖 | 文件 |
|---|---|---|---|---|---|
| **P2** | D1 | Yahoo auto-fetch + TTL 缓存 + **namespace 防撞** | `FetchAndCacheStock(ticker, days)` → 复用 `fetchYahooChart` → PriceStore Save + TTL 检查；ticker 必须前缀（如 `stock_AAPL`），不能直接用 `AAPL` 否则会和 `BTC`/`HS300` 等核心 key 撞 | 保留 key 列表 | `pkg/client/fetch_stock.go` (新建) |
| **P2** | D2 | USStockExtractors | `GenericTechnical + DGS10 + DXY + HY spread + yield curve + VIX` (13 feat, 无 CAPE) | — | `bundles.go` |
| **P2** | D3 | readStock 赋能 | 先查 PriceStore, 无→auto fetch→forecast；切到 `USStockExtractors`；**同时实现 A6**（用 `BuildEquityPanel` 出 panel） | D1+D2 | `main.go` |
| **P2** | D4 | import-stock CLI（**Wave 1**，与 D1 同 wave） | `guanfu import-stock AAPL` → 手动 trigger Yahoo 全量拉取 | D1 | `main.go` |

### D1 TTL 逻辑

```text
FetchAndCacheStock(ticker, days):
  1. 校验 ticker 不在保留 key 列表（btc/qqq/spy/gold/hs300/eth/...）→ 否则 reject
  2. namespacedKey := "stock_" + strings.ToLower(ticker)
  3. PriceStore.Load(namespacedKey)
  4. 有数据且最新日期 ≤1d ago → 返回
  5. 有数据但过期 → 增量 fetch 缺失天数 → PriceStore.Append
  6. 无数据 → Yahoo chart 全量 fetch (days) → PriceStore.Save
```

D1 的 namespace 设计要求 D3/D4 的 `readStock`/`import-stock` 入口在传 ticker 给 PriceStore 时统一通过 `FetchAndCacheStock` 包装，不允许 bypass。

---

## 执行路径

### 第 0 波 — AI 通路解锁 + 低成本修复

每个 weight 改动单独跑回测，便于定位回归源；alias 改动同时落 C4 否则 AI 消费方看不到。

```
A2   ▸ commit 工作树里现有的 sma_200_dev 修复  [独立 commit，让历史可追溯]
A0   ▸ SnapshotData 加 HS300Price 字段          [先于 A1，否则 A1 没字段可写]
C1   ▸ MCP 工具加别名 + 重描述 + alias 单测      [~5 行 dispatch + ~10 行 test]
C4   ▸ SKILL.md 重写                            [文档；与 C1 同 commit]
A1   ▸ Gold/HS300 价格显示修复                   [+5 行 main.go]
A3   ▸ Gold panel DXY 显示（用 fred_dxy.json）   [+~5 行 asset_gold.go]
A4   ▸ HS300 panel 补 PMI/M2/LPR/Northbound      [+~20 行 hs300_dashboard.go，含 dashboard 输入扩展]
B1   ▸ Gold DXY weight 0.45→0.7                 [1 行] ↳ 单独跑回测
B2   ▸ CAPE weight 0.55→0.8                     [1 行] ↳ 单独跑回测
B3   ▸ EquityExtractors 加 VIX                   [+1 行 bundles.go] ↳ 单独跑回测
A5   ▸ QQQ/SPY panel 加 VIX                      [+5 行；与 B3 同 wave 因互依]
```

### 第 1 波 — 任意美股 + 文档

```
D1   ▸ Yahoo auto-fetch + TTL + namespace      [新建 fetch_stock.go, ~80 行]
D2   ▸ USStockExtractors                       [+10 行 bundles.go]
D3   ▸ readStock 升级 + auto-fetch + panel(A6) [~30 行 main.go；A6 合入]
D4   ▸ import-stock CLI                        [+~10 行 main.go]
C2   ▸ get_stock_forecast MCP                  [+~30 行 mcp/main.go；含单测]
C3   ▸ resource/latest 参数化 + list           [+~20 行 mcp/main.go；含单测]
```

### 第 2 波 — 精度 + 文档

```
B5   ▸ Per-asset horizon                       [~10 行；注意不要走 DefaultOptions 路径]
C5   ▸ CLAUDE.md                                [文档；D 系列落地后写]
```

### 待定（需额外调研后决定是否做）

```
B4   ▸ HS300 日频特征                   [先验证 volume 有效性]
B6   ▸ LearnWeights                     [需 RFC 明确技术选型]
C6   ▸ JSON 标准化                      [先列消费方清单，再评破坏性]
```

A6（stock panel）已合入 D3，不再独立 wave。

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

### 回归预算（regression budget）

| 指标 | 容忍下降 | 触发动作 |
|---|---|---|
| 任何 horizon dir hit | ≥3 个百分点 | 回滚该改动，单独再 review |
| PIT 偏离 0.5 加大 | ≥0.05 | 回滚或调权重 |
| 任意资产 backtest 失败 / panic | 任何 | 立即回滚 |

每个 Wave 0 weight 改动**独立提交**，便于 `git revert` 单点回滚。

### 权重总和约束

EquityExtractors 当前权重：CAPE 0.55, DGS10 0.45, DXY 0.45, HY 0.55, Curve 0.45（合 2.45）。Wave 0 同时执行 B2（CAPE→0.8）+ B3（VIX 0.55）后，合 ≈ 3.25，分布严重失衡。

约束方案（任选其一，按 RFC 决定）：

1. **绝对权重无所谓**——kNN 距离归一化已隐式做 normalization，weight 只影响 feature 间的相对权。直接调，回测验证。
2. **再加规一化层**——`forecast.Build` 内部把 weight 总和 rescale 到固定值（例如 2.5）。需要修 `pkg/forecast/distance.go` 的距离计算。

Wave 0 走方案 1（最小代价）；如果回测掉超回归预算，回退到方案 2 走 RFC。

---

## 文件索引

| 文件 | 改动 | 波次 |
|---|---|---|
| `cmd/guanfu/main.go` | printEquityPanel price 显示 (A1), sma_200_dev 修复 commit (A2), readStock 升级 (D3), import-stock CLI (D4) | 0, 1 |
| `cmd/guanfu/akshare_import.go` | 工作树未追踪的 CSI300 一次性导入脚本（`// +build ignore`），是早期 hs300.json 的来源；建议入 git 但加 README 说明它是 ad hoc 工具 | 0 |
| `cmd/guanfu-mcp/main.go` | 工具别名 + 重描述 (C1), resource 参数化 (C3), get_stock_forecast (C2) | 0, 1 |
| `cmd/guanfu-mcp/main_test.go` | alias 单测 (C1) | 0 |
| `pkg/model/types.go` | SnapshotData 加 HS300Price 字段 (A0)，schema version bump | 0 |
| `pkg/engine/asset_gold.go` | BuildPanel 用 fred_dxy.json 补 DXY (A3) | 0 |
| `pkg/engine/asset_hs300.go` | BuildPanel 写 PMI/M2/LPR/Northbound 到 panel.Macro (A4) | 0 |
| `pkg/engine/hs300_dashboard.go` | dashboard 输入扩 PriceStore 句柄或新字段 (A4) | 0 |
| `pkg/engine/asset_qqq.go` | BuildPanel VIX 添加 (A5) | 0 |
| `pkg/engine/asset_spy.go` | BuildPanel VIX 添加 (A5) | 0 |
| `pkg/forecast/features/enhanced.go` | DXY weight (B1), CAPE weight (B2)；(已有 VIX/hy_spread) | 0 |
| `pkg/forecast/features/bundles.go` | EquityExtractors 加 VIX (B3), USStockExtractors (D2) | 0, 1 |
| `pkg/client/fetch_stock.go` | Yahoo auto-fetch + TTL + namespace (D1) (新建) | 1 |
| `skill/SKILL.md` | MCP 工具节 + CLI 更新 (C4)；与 C1 同 commit | 0 |
| `CLAUDE.md` | 项目上下文 (C5) (新建) | 2 |
| `scripts/import_cape.py` | CAPE ingestion | — |

---

## 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-05-08 | 初版，4 Track x 22 项 |
| v2 | 2026-05-08 | 加 Dependency 列；C1 加别名不改名；B4 降 P3 + 加验证说明；B6 摘出需 RFC；D1 加 TTL；执行顺序重排为 0/1/2/待定 四波 |
| v3 | 2026-05-08 | Review 落地修复：(1) **A0 新增** SnapshotData schema 改动作为 A1 前置；(2) **A2 修订**——working tree 改动需独立 commit；(3) **A3/A4 修订**——纠正"需要数据 ingestion"的误读，数据已在 PriceStore，只缺 panel 显示；(4) **A6 合入 D3**，不再独立；(5) **C1 加 alias 单测**要求；(6) **C4 与 C1 同 commit**；(7) **D1 加 namespace 防撞**；(8) **D4 显式 Wave 1**；(9) **新增"回归预算"** + **"权重总和约束"** 两节；(10) **B5 加实现注**（DefaultOptions 覆盖陷阱）；(11) **B1/B2 PIT 方向澄清**；(12) **C5 audience 注**；(13) **C6 要求消费方清单先行**；(14) **akshare_import.go 入文件索引**；(15) Wave 0 weight 改动**单独提交单独回测**。 |
