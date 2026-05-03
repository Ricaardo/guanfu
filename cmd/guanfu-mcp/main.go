// guanfu-mcp — 观复 MCP Server
//
// 通过 stdio 提供 MCP 协议接口，让 Claude Desktop / Cursor / 任何 MCP 客户端
// 直接调用 guanfu 引擎获取 BTC 盘面数据，无需 Bash 权限。
//
// 提供的 Tools:
//   get_btc_panel       — 完整 8 域盘面 (JSON)
//   get_btc_verdict     — 结构化多域读盘 (JSON)
//   get_domain          — 单个域 (cycle/valuation/network/...)
//   get_indicator       — 单个指标 (ahr999, hash_ribbons, ...)
//
// 提供的 Resources:
//   guanfu://knowledge/skill.md — SKILL.md 知识库
//   guanfu://panel/latest       — 缓存的最新盘面
//   guanfu://verdict/latest     — 缓存盘面的结构化读盘
//
// 部署: 在 claude_desktop_config.json 中添加:
//
//	{
//	  "mcpServers": {
//	    "guanfu": {
//	      "command": "/path/to/guanfu-mcp",
//	      "env": {
//	        "GUANFU_HISTORY_DB": "/path/to/history.db",
//	        "GUANFU_SKILL_PATH": "/path/to/skill/SKILL.md",
//	        "FUTU_GATEWAY": "127.0.0.1:11111"
//	      }
//	    }
//	  }
//	}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Ricaardo/guanfu/internal/client"
	"github.com/Ricaardo/guanfu/internal/engine"
	"github.com/Ricaardo/guanfu/internal/history"
	"github.com/Ricaardo/guanfu/internal/model"
	"github.com/Ricaardo/guanfu/internal/version"
)

// ─── JSON-RPC types ───────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── MCP tool definitions ─────────────────────────────

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

var tools = json.RawMessage(`
[
  {
    "name": "get_btc_panel",
    "description": "获取 BTC 完整 8 域 40+ 指标盘面。包含周期/估值/网络/杠杆/宏观/资金流/技术/跨资产。每个指标含原始值、历史分位(q)、解读标签、数据源。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      }
    }
  },
  {
    "name": "get_btc_verdict",
    "description": "获取 BTC 结构化多域读盘。基于完整盘面输出域级一致性、覆盖率、风险状态、顶/底接近度、证据和失效条件；不输出交易指令或仓位指令。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"},
        "include_panel": {"type": "boolean", "description": "是否同时返回原始指标盘面，默认 false"}
      }
    }
  },
  {
    "name": "get_domain",
    "description": "获取单个域的指标。domain 可选: cycle, valuation, network, positioning, macro, flow, technical, cross_asset。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "domain": {"type": "string", "enum": ["cycle","valuation","network","positioning","macro","flow","technical","cross_asset"]},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      },
      "required": ["domain"]
    }
  },
  {
    "name": "get_indicator",
    "description": "获取单个指标的值。name 可选: ahr999, mayer_multiple, mvrv_z_score, hash_ribbons, funding_rate_pct, fear_greed, etf_net_flow_30d_usd, rsi_14, btc_gold_ratio, 等。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "name": {"type": "string", "description": "指标 key 名称，如 ahr999, hash_ribbons, fear_greed"},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      },
      "required": ["name"]
    }
  }
]`)

// ─── Main ──────────────────────────────────────────────

var (
	panelCache    *model.IndicatorPanel
	panelCacheMu  sync.RWMutex
	panelCacheTTL = 5 * time.Minute
	panelCacheAt  time.Time
)

const defaultPanelTimeout = 90 * time.Second

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	cacheTTL := flag.Duration("cache-ttl", panelCacheTTL, "in-memory panel cache TTL")
	flag.Parse()
	if *showVersion {
		version.Print(os.Stdout, "guanfu-mcp")
		return
	}
	if *cacheTTL > 0 {
		panelCacheTTL = *cacheTTL
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	writer := os.Stdout

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		resp := handleRequest(&req)
		if resp == nil {
			continue
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(writer, string(data))
	}
}

func handleRequest(req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]bool{},
				"resources": map[string]bool{},
			},
			"serverInfo": map[string]any{
				"name":    "guanfu",
				"version": "3.1.0",
			},
		})

	case "notifications/initialized":
		return nil // no response for notifications

	case "tools/list":
		return ok(req.ID, map[string]any{"tools": json.RawMessage(tools)})

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid tools/call params: "+err.Error())
		}
		result, rpcErr := handleToolCall(params.Name, params.Arguments)
		if rpcErr != nil {
			return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
		}
		return ok(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": result}}})

	case "resources/list":
		return ok(req.ID, map[string]any{"resources": json.RawMessage(`[
			{"uri":"guanfu://knowledge/skill.md","name":"SKILL.md","mimeType":"text/markdown","description":"观复 BTC 投资盘面解读知识库"},
			{"uri":"guanfu://panel/latest","name":"最新盘面","mimeType":"application/json","description":"缓存的最新完整盘面 JSON"},
			{"uri":"guanfu://verdict/latest","name":"最新结构化读盘","mimeType":"application/json","description":"缓存盘面的 verdict JSON"}
		]`)})

	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid resources/read params: "+err.Error())
		}
		content, rpcErr := handleResourceRead(params.URI)
		if rpcErr != nil {
			return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
		}
		return ok(req.ID, map[string]any{"contents": []map[string]any{{
			"uri":      params.URI,
			"mimeType": resourceMimeType(params.URI),
			"text":     content,
		}}})

	default:
		return errResp(req.ID, -32601, "unknown method: "+req.Method)
	}
}

func handleToolCall(name string, args json.RawMessage) (string, *rpcError) {
	switch name {
	case "get_btc_panel":
		timeout, rpcErr := timeoutFromArgs(args)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(timeout)
		b, _ := json.MarshalIndent(panel, "", "  ")
		return string(b), nil

	case "get_btc_verdict":
		var p struct {
			TimeoutSeconds int  `json:"timeout_seconds,omitempty"`
			IncludePanel   bool `json:"include_panel,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(timeout)
		b, _ := json.MarshalIndent(buildVerdictPayload(panel, p.IncludePanel), "", "  ")
		return string(b), nil

	case "get_domain":
		var p struct {
			Domain         string `json:"domain"`
			TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		if !validDomain(p.Domain) {
			return "", &rpcError{Code: -32602, Message: "invalid domain: " + p.Domain}
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(timeout)
		dom := getDomainJSON(panel, p.Domain)
		b, _ := json.MarshalIndent(dom, "", "  ")
		return string(b), nil

	case "get_indicator":
		var p struct {
			Name           string `json:"name"`
			TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		if p.Name == "" {
			return "", &rpcError{Code: -32602, Message: "indicator name is required"}
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(timeout)
		ind := findIndicator(panel, p.Name)
		if ind != nil {
			b, _ := json.MarshalIndent(ind, "", "  ")
			return string(b), nil
		}
		return "", &rpcError{Code: -32602, Message: fmt.Sprintf("indicator %q not found", p.Name)}

	default:
		return "", &rpcError{Code: -32601, Message: "unknown tool: " + name}
	}
}

func handleResourceRead(uri string) (string, *rpcError) {
	switch uri {
	case "guanfu://knowledge/skill.md":
		data, err := os.ReadFile(skillPath())
		if err != nil {
			return "", &rpcError{Code: -32603, Message: "SKILL.md not found: " + err.Error()}
		}
		return string(data), nil
	case "guanfu://panel/latest":
		panel := getOrFetchPanel(defaultPanelTimeout)
		b, _ := json.MarshalIndent(panel, "", "  ")
		return string(b), nil
	case "guanfu://verdict/latest":
		panel := getOrFetchPanel(defaultPanelTimeout)
		b, _ := json.MarshalIndent(buildVerdictPayload(panel, false), "", "  ")
		return string(b), nil
	default:
		return "", &rpcError{Code: -32602, Message: "unknown resource: " + uri}
	}
}

func resourceMimeType(uri string) string {
	switch uri {
	case "guanfu://knowledge/skill.md":
		return "text/markdown"
	case "guanfu://panel/latest", "guanfu://verdict/latest":
		return "application/json"
	default:
		return "text/plain"
	}
}

func buildVerdictPayload(panel *model.IndicatorPanel, includePanel bool) any {
	verdict := engine.BuildVerdict(panel)
	if !includePanel {
		return verdict
	}
	return struct {
		Verdict *engine.Verdict       `json:"verdict"`
		Panel   *model.IndicatorPanel `json:"panel"`
	}{
		Verdict: verdict,
		Panel:   panel,
	}
}

// ─── Panel fetch with cache ───────────────────────────

func getOrFetchPanel(timeout time.Duration) *model.IndicatorPanel {
	panelCacheMu.RLock()
	if panelCache != nil && time.Since(panelCacheAt) < panelCacheTTL {
		defer panelCacheMu.RUnlock()
		return panelCache
	}
	panelCacheMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	provider := client.NewRealClient()
	snap, err := provider.GetSnapshot(ctx)
	if err != nil {
		return &model.IndicatorPanel{Date: "error", StaleWarnings: []string{err.Error()}}
	}

	cfg := &model.Config{}
	calc := engine.NewCalculator(cfg)
	if os.Getenv("GUANFU_NO_HISTORY") != "1" {
		store, err := history.Open(os.Getenv("GUANFU_HISTORY_DB"))
		if err != nil {
			log.Printf("history.Open failed (continuing without history quantiles): %v", err)
		} else {
			defer store.Close()
			calc = calc.WithHistory(store)
		}
	}
	panel := calc.BuildPanel(snap)

	panelCacheMu.Lock()
	panelCache = panel
	panelCacheAt = time.Now()
	panelCacheMu.Unlock()

	return panel
}

// ─── Helpers ──────────────────────────────────────────

func getDomainJSON(p *model.IndicatorPanel, domain string) map[string]model.Indicator {
	switch domain {
	case "cycle":
		return p.Cycle
	case "valuation":
		return p.Valuation
	case "network":
		return p.Network
	case "positioning":
		return p.Positioning
	case "macro":
		return p.Macro
	case "flow":
		return p.Flow
	case "technical":
		return p.Technical
	case "cross_asset":
		return p.CrossAsset
	default:
		return nil
	}
}

func validDomain(domain string) bool {
	switch domain {
	case "cycle", "valuation", "network", "positioning", "macro", "flow", "technical", "cross_asset":
		return true
	default:
		return false
	}
}

func findIndicator(p *model.IndicatorPanel, name string) *model.Indicator {
	for _, dom := range []map[string]model.Indicator{p.Cycle, p.Valuation, p.Network, p.Positioning, p.Macro, p.Flow, p.Technical, p.CrossAsset} {
		if ind, ok := dom[name]; ok {
			return &ind
		}
	}
	return nil
}

func skillPath() string {
	if p := os.Getenv("GUANFU_SKILL_PATH"); p != "" {
		return p
	}
	return "skill/SKILL.md"
}

// ─── RPC helpers ──────────────────────────────────────

func ok(id any, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id any, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func decodeArgs(args json.RawMessage, dest any) *rpcError {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, dest); err != nil {
		return &rpcError{Code: -32602, Message: "invalid tool arguments: " + err.Error()}
	}
	return nil
}

func timeoutFromArgs(args json.RawMessage) (time.Duration, *rpcError) {
	var p struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if rpcErr := decodeArgs(args, &p); rpcErr != nil {
		return 0, rpcErr
	}
	return timeoutFromSeconds(p.TimeoutSeconds)
}

func timeoutFromSeconds(seconds int) (time.Duration, *rpcError) {
	if seconds < 0 {
		return 0, &rpcError{Code: -32602, Message: "timeout_seconds must be non-negative"}
	}
	if seconds == 0 {
		return defaultPanelTimeout, nil
	}
	if seconds > 300 {
		return 0, &rpcError{Code: -32602, Message: "timeout_seconds must be <= 300"}
	}
	return time.Duration(seconds) * time.Second, nil
}
