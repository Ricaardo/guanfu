# btc-guanfu skill (v3)

观复 — 多资产投资盘面 + 解读知识库的 Claude Code skill 包。

安装：

```bash
ln -s "$(pwd)/skill" ~/.claude/skills/btc-guanfu
```

内容：

| 文件 | 用途 |
|---|---|
| `SKILL.md` | 完整版 ~850 行：指标手册（定义 + 阈值 + 失效情形）、知识库指针、读盘工作流、图表代码 |
| `tier1.md` | **数据契约 + 可靠性表 + 关键阈值** — AI 每次读盘必载（~200 行）|
| `tier2.md` | 决策框架 + 域级方向规则 + 行为护栏 + 输出模板 — 做判断时读（~160 行） |
| `kb/` | 10 个因果推理文件（宏观传导 / 危机 playbook / 历史类比等）|

推荐加载顺序：`guanfu://skill/tier1`（必载）→ `guanfu://skill/tier2`（决策时）→ `SKILL.md` 对应节（追问细节时）。
