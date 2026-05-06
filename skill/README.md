# btc-guanfu skill (v2)

观复 / 多资产投资盘面 + 解读知识库的 Claude Code skill 包。

`SKILL.md` 是数据契约 + 指标手册，`kb/` 是 10 个因果推理文件（宏观传导、流动性管道、加密结构、跨资产、地缘冲击、regime taxonomy、历史类比、决策矩阵、危机 playbook）。

## v2 新增

- 多资产支持 (BTC/QQQ/SPY/Gold/CSI300)
- kNN 走势推演 (--forecast / --forecast-path)
- DCA 定投策略对比 (guanfu dca)
- 懒人组合配置 (guanfu allocate)
- 多资产回测 (guanfu backtest all)
- MCP tools 支持 asset 参数

## 安装

```bash
ln -s "$(pwd)/skill" ~/.claude/skills/btc-guanfu
```

或 sparse checkout：

```bash
git clone --depth=1 --filter=blob:none --sparse https://github.com/Ricaardo/guanfu.git
cd guanfu && git sparse-checkout set skill
ln -s "$(pwd)/skill" ~/.claude/skills/btc-guanfu
```

## 触发

用户问「BTC/QQQ/SPY/黄金/沪深300 该不该买/卖」「估值如何」「顶/底」「定投」「AHR999/MVRV」「周期位置」「观复」时自动激活。
