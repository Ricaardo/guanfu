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
