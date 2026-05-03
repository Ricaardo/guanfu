# 跨资产联动 (Cross-Asset Transmission)

> 解释 BTC 与黄金、股票、美元、波动率和油价 proxy 的关系。实时值和 source 以 `cross_asset` / `macro` 域为准。

---

## 适用时机

当以下情况出现时加载本文：

- `btc_spy_corr_30d` 或 `spx_correlation_30d` 明显升高/降低。
- `btc_gold_ratio` 接近历史极端。
- `rel_strength_90d_gold` 显示 BTC 显著跑赢/跑输黄金。
- `uup_price`、`vixy_price`、`oil_proxy_usd` 或 `wti_crude_usd` 出现异常。
- BTC 与 SPX/Gold 走势方向冲突。

---

## 数据源边界

- 黄金主源通常是 Binance PAXG；GLD 是 Futu 扩展数据。
- QQQ/SPY/UUP/VIXY/GLD/USO 通常来自 Futu，失败时部分资产可走 Yahoo fallback。
- `oil_proxy_usd` 是 USO ETF proxy；`wti_crude_usd` 才是 WTI 近月期货。不要混用。
- 任何跨资产比率都要先看 `source` 和 `updated_at`。

---

## BTC vs 黄金

### 关系框架

| 时间尺度 | 常见关系 | 解释 |
|---|---|---|
| 年 | 同受法币贬值/流动性影响 | 共享货币属性叙事 |
| 月 | 风险偏好轮动 | risk-on 时 BTC 跑赢，避险时黄金跑赢 |
| 日 | 可同涨同跌也可背离 | 取决于冲击类型 |

### `btc_gold_ratio`

| 区间 | 解读 |
|---|---|
| `<5` | BTC 极弱，历史深熊区 |
| `5-10` | BTC 偏弱 |
| `10-20` | 中性 |
| `20-30` | BTC 偏强 |
| `>30` | BTC 极强，风险偏好可能过热 |

组合优先于单点：

- `btc_gold_ratio <10` + 矿工投降结束：底部确认增强。
- `btc_gold_ratio >30` + funding/OI 过热：顶部风险增强。
- 黄金上涨、BTC 下跌、SPX 下跌：BTC 未被市场当作避险资产，需要降权“数字黄金”叙事。

---

## BTC vs 美股

### 相关性不是方向

`btc_spy_corr_30d` / `spx_correlation_30d` 表示解释变量权重：

| 相关性 | 含义 | 读盘重点 |
|---|---|---|
| `<0` | 独立或反向 | 查 ETF、监管、加密内部事件 |
| `0-0.3` | 弱相关 | BTC 自身叙事权重高 |
| `0.3-0.7` | 中等相关 | 宏观和内部共同作用 |
| `>0.7` | 强风险资产联动 | SPX、美元、实际利率优先 |

### 四象限

| SPX | BTC | 解释 |
|---|---|---|
| 上涨 | 上涨 | 风险偏好一致，增量信息有限 |
| 下跌 | 下跌 | 宏观去风险，需区分流动性冲击还是基本面恶化 |
| 下跌 | 上涨 | BTC 独立叙事强，偏正面 |
| 上涨 | 下跌 | 加密内部问题或 BTC 相对弱，需查事件 |

---

## BTC vs 美元

UUP 是 DXY proxy，不等同于 FRED `DTWEXBGS`。使用方式：

- UUP 上涨 + `dxy_60d_trend_pct` 上行：美元逆风更可信。
- UUP 上涨但 BTC 也涨：可能是独立叙事或全球避险并存，需看黄金和 SPX。
- UUP 数据 stale 时，优先用 FRED 60 日趋势，但承认滞后。

---

## 波动率与恐慌

VIXY 是 VIX 短期期货 ETF proxy，存在期货展期损耗，不等同于 VIX 点位。

读盘规则：

- `vixy_price` 或外部 VIX 高位 + BTC/SPX 相关高：系统性恐慌传导概率上升。
- VIX 高但 BTC 抗跌：BTC 独立性增强，但需要 ETF/稳定币确认。
- VIX 平静 + funding/OI 过热：市场自满，尾部风险上升。

若进入极端恐慌，转 `09-crisis-playbook.md`。

---

## Oil proxy / WTI

油价影响 BTC 的路径通常是：

```
油价/能源冲击 → 通胀预期 → Fed 反应 → 实际利率/美元 → BTC
```

读盘规则：

- `oil_proxy_60d_trend_pct` 或 `wti_crude_60d_trend_pct` 上行本身不是 BTC 利空；要看是否推升实际利率和美元。
- `wti_crude_usd >100` + `real_yield >2%` + SPX 下跌：滞胀压力，需要降低常规模型置信。
- source 为 `futu:US.USO` 时，只能说油价 proxy，不说桶价。

---

## 和 guanfu 联动

每次跨资产分析回答四个问题：

1. BTC 当前是宏观联动还是自身叙事？
2. 如果宏观联动，主导变量是股票、美元、利率、黄金还是油价？
3. BTC 相对黄金和 SPX 是强还是弱？
4. 数据源是否 stale、fallback 或缺失？

输出时用“跨资产证据支持/反证某结论”，不要让单个相关性直接决定方向。
