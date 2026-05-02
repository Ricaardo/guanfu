# guanfu MCP Server 部署

## Claude Desktop

编辑 `claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "guanfu": {
      "command": "/Users/x/guanfu/bin/guanfu-mcp",
      "env": {
        "FRED_API_KEY": "your_fred_key",
        "GUANFU_NO_HISTORY": "1",
        "FUTU_GATEWAY": "127.0.0.1:11111"
      }
    }
  }
}
```

重启 Claude Desktop，对话中直接问 "BTC 现在怎么样？" 即可。

## Claude Code

Claude Code 已有 btc-guanfu skill（CLI JSON 方式），MCP 可作为补充：在 `.claude/mcp.json` 中添加同上配置。

## Cursor / Windsurf / 任意 MCP 客户端

同上，配置 `mcpServers.guanfu` 指向二进制路径。

## 提供的 Tools

| Tool | 用途 |
|------|------|
| `get_btc_panel` | 完整 8 域 42 指标 JSON |
| `get_domain` | 单个域（cycle/valuation/...） |
| `get_indicator` | 单个指标值 + 分位 |

## 提供的 Resources

| URI | 内容 |
|-----|------|
| `guanfu://knowledge/skill.md` | SKILL.md 知识库 |
| `guanfu://panel/latest` | 缓存的最新盘面 |
