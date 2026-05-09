# guanfu 内部规范

> 面向 maintainer / AI coder,不是用户。记录几个隐式约定,避免未来重构踩坑。

---

## 1. Feature weight 归一化策略(I3)

### 现状

`pkg/forecast/features/bundles.go` 每个 extractor 函数返回 `[]FeatureValue`,每个带 `Weight` 字段。kNN 距离计算里:

```go
// pkg/forecast/forecast.go: distance()
for _, av := range a.values {
    bv := b.byName[av.Name]
    diff := av.Normalized - bv.Normalized
    sum += av.Weight * diff * diff
    weightSum += av.Weight
    matched++
}
return math.Sqrt(sum / weightSum), matched, true
```

**关键点**:距离里分母是 `weightSum`(匹配到的权重总和),不是 bundle 里所有权重的总和。也就是说:
- **Weight 的绝对值不重要,相对权重才重要**。把所有 weight × 2 得到的 kNN 排序完全一致。
- **但不同 bundle 的 weight 总和差异会体现在 coverage 度量上**(G3 probe 公式)。

### 为什么不显式归一化

曾考虑在 `Build()` 内部把 weight 按 `Σ=1` 归一化。结论:**不做**,原因:

1. 把调参的自由度留给 maintainer。显式归一化后,要降一个 feature 的权重必须提高其他所有 feature,改一处等于改整个 bundle。
2. 目前回测稳。v2 B2/B3 波加 VIX (0.55) + CAPE → 0.8 后 EquityExtractors 权重总和从 2.45 升到 3.25,分布明显变化;回测未恶化。证明当前非归一化策略**具备鲁棒性**。
3. G3 coverage 改为 weight 比率后,跨 bundle 比较的 coverage 已经 apples-to-apples(Σ(有效 weight)/Σ(bundle 总 weight))—— 没有再次归一化的必要。

### 新 feature 加权原则

- 新 feature 初始 weight 默认 **0.5**(居中,不强调)
- 跑 `TestBacktestBundles`,看 dir_hit 变化:
  - ≥ +2pp → 权重可加到 **0.7-1.0**,继续观察
  - 持平 → 保留 **0.5**,不硬推
  - ≤ -2pp → 删除,别留负贡献
- 如果新 feature 导致某个 bundle 的**某些 horizon dir_hit 下降 ≥ 3pp**,触发回归预算(见 `docs/archive/v3/guanfu-v3-todo.md`),先回滚再决定

### 何时走 RFC 引入显式归一化

如果有一天加 feature 导致 EquityExtractors 权重总和 > 5(当前 3.25 的 1.5 倍),或某个 bundle 的单 feature weight 占比超过 **40%** (当前 CAPE 0.8 / 3.25 ≈ 25% 是上限),则需要 RFC 决定是否统一归一化到 Σ=2.5 左右。

---

## 2. BTC ad-hoc 源与 refresh 框架(I4)

### 现状

`guanfu refresh` 注册了 23 个 `Source`,但 BTC 面板依赖的几个数据**不走 refresh 框架**:

| 源 | 走哪 | TTL |
|---|---|---|
| CoinGecko 总市值 / BTC 市占率 / 稳定币市值 | `pkg/client/coingecko.go` 即时拉 | 内存缓存 |
| mempool.space 哈希率 / 难度 / mempool | `pkg/client/mempool.go` 即时拉 | 内存缓存 |
| alternative.me 恐慌贪婪 | `pkg/client/fng.go` 即时拉 | 内存缓存 |
| SoSoValue BTC ETF 净流入 | `pkg/client/sosovalue.go` 即时拉 | 内存缓存 |
| Binance Top50 kline / 资金费率 / OI | `pkg/client/binance.go` 即时拉 | 内存缓存 |
| CoinMetrics community MVRV / NUPL | `pkg/client/coinmetrics.go` 即时拉 | 内存缓存 |

### 为什么当前没纳入 refresh

这些源的**访问频率特点**都是 "每次跑 BTC panel 就拉一次,过程不长",与 `refresh` 强调的 "可中断、分类诊断、增量 vs 全量" 架构没有强匹配。强行塞进去:

1. 要给每个源做 `Source` 接口实现,每个至少 80 行胶水代码 × 6 = 500 行净增,不抵收益
2. 内存缓存足够;磁盘 archive 意义不大(不像 FRED/Yahoo 那样需要历史分位)
3. 真正有磁盘 archive 需要的是 **history.db 里的 15 个非价格指标**,那套机制已经独立存在

### 纳入 refresh 的前置条件

什么情况下值得改成 `Source`:
- 源的 API 限流到让 panel-build 被拖慢(目前 90s 冷启动可接受)
- 用户要求 `guanfu refresh` 一次搞定所有数据(目前 23 个 source 之外的都靠 panel-build 触发拉取)
- 新加 BTC 链上特征需要多年历史而非即时值

短期内不做。**记录在此**,未来如果有人发起要改,先看这段再说。

---

## 3. Schema 演化(I5)

guanfu 有 4 种被持久化 / 跨版本保留的 struct,各有独立策略:

### `model.MarketSnapshot` — `CurrentMarketSnapshotSchemaVersion`

用途:BTC 市场快照磁盘缓存(`cache/market_cache.json`)。

| 改动类型 | 是否 bump |
|---|---|
| 加**新可选字段**(`omitempty` / 零值兼容) | **不 bump**;老缓存继续 unmarshal 成功,缺失字段取零值 |
| 加**新必需字段**(空时破坏逻辑) | **bump**;老缓存触发 drop,下次冷启动重拉 |
| 删字段 / 改类型 / 改语义 | **bump**,同上 |

当前 `CurrentMarketSnapshotSchemaVersion` = 某个整数(见 `pkg/model/types.go`),每次 bump 写入 commit 信息。

### `model.SnapshotData`

用途:`IndicatorPanel.Snapshot` 字段 —— **不落盘**,每次 panel 构造时重填。

- 加字段**永远不 bump**(没有磁盘读者)
- 只需保证 JSON 消费方(SKILL / MCP)接新字段是向后兼容的 → 全 `omitempty`

v2 A0 犯过一次错:把 `HS300Price` 加到 `SnapshotData` 时,曾经以为要 bump `CurrentMarketSnapshotSchemaVersion`。**不用**,原因是这两个 struct 关系如下:

```
SnapshotData    (live panel)  — rebuilt every run, no disk serialization
MarketSnapshot  (BTC cache)   — serialized to cache/market_cache.json
```

### `claim.Claim` / `claim.Intent` — `claim.SchemaVersion` = 1

用途:`~/.guanfu/claims/` 持久 ledger。

- **加可选字段**:不 bump,老 claim 文件继续 unmarshal 成功(缺失字段 = 零值)
- **必需字段 / 改语义**:bump + 为老版本写 migration 或显式声明兼容性
- 不做自动 migration —— claim 是只读 append-only,每次 read 时 schema version 低于当前就当成"历史数据按 v1 语义解读"

当前 `SchemaVersion = 1`。bump 到 2+ 要在 roadmap 记录触发原因。

### `portfolio.Portfolio` — `portfolio.SchemaVersion` = 1

用途:`~/.guanfu/portfolio.json` 用户 opt-in 配置。

- `Validate()` 会 reject `schema_version > SchemaVersion`(未来版本,当前 guanfu 不认识)
- 未来加字段:新字段必须 optional + 兼容 SchemaVersion=1

### 共同原则

1. **加 optional 字段永远 ok**,不 bump
2. **Breaking change 必须 bump + 记在变更日志**
3. **migration 脚本不维护**,靠 schema_version 检查 → "不兼容就忽略老数据" 比 "写迁移逻辑" 更稳
4. **consumer 必须处理 `SchemaVersion == 0`**(老数据无字段时默认为 1)

---

## 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-05-09 | 首版,覆盖 I3 weight 归一化 / I4 BTC ad-hoc 源 / I5 schema 演化 3 个隐式约定 |
| v2 | 2026-05-09 | I6 追加:`pkg/engine/calculator.go` 有 ~500 行 v1 死代码待清理(`Calculate()` 方法 + `ScoreResult` 类型 + 11 个仅它用的 calc helper,全部 v1 NewsEngine Discord/Feishu 推送遗留)。零外部引用,不阻塞运行。下一轮维护时单独 commit 清理 |
