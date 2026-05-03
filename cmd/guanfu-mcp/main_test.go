package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/internal/model"
)

func TestInitializedNotificationHasNoResponse(t *testing.T) {
	resp := handleRequest(&rpcRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	if resp != nil {
		t.Fatalf("expected no response for notification, got %+v", resp)
	}
}

func TestHandleToolCallRejectsInvalidDomain(t *testing.T) {
	_, rpcErr := handleToolCall("get_domain", json.RawMessage(`{"domain":"bad"}`))
	if rpcErr == nil {
		t.Fatal("expected invalid domain error")
	}
	if rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "invalid domain") {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
}

func TestHandleToolCallRejectsInvalidTimeout(t *testing.T) {
	_, rpcErr := handleToolCall("get_btc_panel", json.RawMessage(`{"timeout_seconds":-1}`))
	if rpcErr == nil {
		t.Fatal("expected invalid timeout error")
	}
	if rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "timeout_seconds") {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
}

func TestToolsListIncludesVerdictAndAvoidsFixedIndicatorCount(t *testing.T) {
	resp := handleRequest(&rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}
	body := string(b)
	if !strings.Contains(body, "get_btc_verdict") {
		t.Fatalf("expected get_btc_verdict in tools/list: %s", body)
	}
	if strings.Contains(body, "42 个指标") || strings.Contains(body, "44 指标") {
		t.Fatalf("tools/list should not advertise stale fixed indicator counts: %s", body)
	}
}

func TestHandleToolCallGetBTCVerdictUsesCachedPanel(t *testing.T) {
	setCachedPanelForTest(t, &model.IndicatorPanel{
		Date:        "2026-05-03",
		Cycle:       map[string]model.Indicator{"mayer_multiple": {Value: 0.7}},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	})

	out, rpcErr := handleToolCall("get_btc_verdict", json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatalf("get_btc_verdict returned error: %+v", rpcErr)
	}
	if !strings.Contains(out, `"net_direction"`) || !strings.Contains(out, `"regime"`) {
		t.Fatalf("expected verdict JSON, got %s", out)
	}
}

func TestHandleResourceReadVerdictLatest(t *testing.T) {
	setCachedPanelForTest(t, &model.IndicatorPanel{
		Date:        "2026-05-03",
		Cycle:       map[string]model.Indicator{"mayer_multiple": {Value: 0.7}},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	})

	out, rpcErr := handleResourceRead("guanfu://verdict/latest")
	if rpcErr != nil {
		t.Fatalf("verdict resource returned error: %+v", rpcErr)
	}
	if !strings.Contains(out, `"net_direction"`) {
		t.Fatalf("expected verdict JSON, got %s", out)
	}
}

func TestHandleRequestResourceReadUsesDeclaredMimeType(t *testing.T) {
	setCachedPanelForTest(t, &model.IndicatorPanel{
		Date:        "2026-05-03",
		Cycle:       map[string]model.Indicator{"mayer_multiple": {Value: 0.7}},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	})

	resp := handleRequest(&rpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "resources/read",
		Params:  json.RawMessage(`{"uri":"guanfu://verdict/latest"}`),
	})
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected resources/read result: %#v", resp.Result)
	}
	contents, ok := result["contents"].([]map[string]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("unexpected resources/read contents: %#v", result["contents"])
	}
	if got := contents[0]["mimeType"]; got != "application/json" {
		t.Fatalf("expected JSON mime type for verdict resource, got %#v", got)
	}
	text, ok := contents[0]["text"].(string)
	if !ok || !strings.Contains(text, `"net_direction"`) {
		t.Fatalf("expected verdict JSON payload, got %#v", contents[0]["text"])
	}
}

func TestResourceMimeTypeMatchesDeclaredResources(t *testing.T) {
	cases := map[string]string{
		"guanfu://knowledge/skill.md": "text/markdown",
		"guanfu://panel/latest":       "application/json",
		"guanfu://verdict/latest":     "application/json",
		"guanfu://unknown":            "text/plain",
	}
	for uri, want := range cases {
		if got := resourceMimeType(uri); got != want {
			t.Fatalf("resourceMimeType(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestTimeoutDefaultsAndBounds(t *testing.T) {
	got, rpcErr := timeoutFromSeconds(0)
	if rpcErr != nil {
		t.Fatalf("default timeout returned error: %+v", rpcErr)
	}
	if got != defaultPanelTimeout {
		t.Fatalf("expected default %s, got %s", defaultPanelTimeout, got)
	}

	got, rpcErr = timeoutFromSeconds(7)
	if rpcErr != nil {
		t.Fatalf("custom timeout returned error: %+v", rpcErr)
	}
	if got != 7*time.Second {
		t.Fatalf("expected 7s, got %s", got)
	}

	_, rpcErr = timeoutFromSeconds(301)
	if rpcErr == nil {
		t.Fatal("expected upper-bound timeout error")
	}
}

func setCachedPanelForTest(t *testing.T, panel *model.IndicatorPanel) {
	t.Helper()
	panelCacheMu.Lock()
	oldPanel := panelCache
	oldAt := panelCacheAt
	panelCache = panel
	panelCacheAt = time.Now()
	panelCacheMu.Unlock()

	t.Cleanup(func() {
		panelCacheMu.Lock()
		panelCache = oldPanel
		panelCacheAt = oldAt
		panelCacheMu.Unlock()
	})
}
