# btc-guanfu skill

观复 / 观察万物之周期回归 — BTC 投资盘面 + 解读知识库的 Claude Code skill 包。

`SKILL.md` 是数据契约 + 指标手册，`kb/` 是 10 个因果推理文件（宏观传导、流动性管道、加密结构、跨资产、地缘冲击、regime taxonomy、历史类比、决策矩阵、危机 playbook、数据契约定义）。

## 安装

需要 Claude Code（>= 1.x）。把这个目录放到 `~/.claude/skills/btc-guanfu/`：

```bash
# 选项 A：symlink（推荐，跟随 git 更新）
ln -s "$(pwd)/skill" ~/.claude/skills/btc-guanfu

# 选项 B：拷贝
cp -R skill ~/.claude/skills/btc-guanfu
```

只想拉这一个 skill、不要整个 guanfu 仓库：

```bash
git clone --depth=1 --filter=blob:none --sparse https://github.com/Ricaardo/guanfu.git
cd guanfu && git sparse-checkout set skill
ln -s "$(pwd)/skill" ~/.claude/skills/btc-guanfu
```

## 数据来源

skill 解读的盘面 JSON 由 [guanfu](https://github.com/Ricaardo/guanfu) CLI 或 MCP server 产出。skill 本身不抓数据 — 它是解读层，吃 panel.json 输出读盘结论。

最小用法：

```bash
guanfu --json > panel.json
# 在 Claude Code 里："读这个盘面"，附上 panel.json
```

或配置 MCP server，Claude Code 自动调 `guanfu://panel/json` resource。见仓库根的 `docs/mcp-setup.md`。

## 触发

用户问「BTC 该不该买/卖」「比特币现在估值如何」「加密底/顶在哪」「定投区吗」「AHR999/MVRV/哈希率/ETF 流入多少」「BTC 周期位置」「观复」时自动激活。

详细触发条件见 `SKILL.md` 顶部 frontmatter。

## 不做什么

- 不输出单一评分或无上下文交易指令
- 不提供财务建议；输出基于多维指标一致性的概率加权读盘结论，每条带证据链、反证和失效条件
- altcoin/memecoin → 用 `cmc-mcp` / `okx-dex`；K 线形态 → `technical-analysis`；链上钱包 → `okx-wallet`
