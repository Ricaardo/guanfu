package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/pkg/model"
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
	// Both new (preferred) and old (deprecated alias) tool names must be discoverable.
	for _, want := range []string{
		"get_panel", "get_verdict", "get_forecast",
		"get_btc_panel", "get_btc_verdict", "get_btc_forecast",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %s in tools/list: %s", want, body)
		}
	}
	if strings.Contains(body, "42 个指标") || strings.Contains(body, "44 指标") {
		t.Fatalf("tools/list should not advertise stale fixed indicator counts: %s", body)
	}
}

// C2: get_stock_forecast must reject empty/missing ticker before any
// network or store work. Catches dispatch-side validation regressions.
func TestHandleToolCallGetStockForecastRequiresTicker(t *testing.T) {
	cases := []string{
		`{}`,
		`{"ticker":""}`,
		`{"ticker":"   "}`,
	}
	for _, args := range cases {
		_, rpcErr := handleToolCall("get_stock_forecast", json.RawMessage(args))
		if rpcErr == nil {
			t.Errorf("get_stock_forecast(%s) expected error, got nil", args)
			continue
		}
		if rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "ticker") {
			t.Errorf("get_stock_forecast(%s) unexpected error: %+v", args, rpcErr)
		}
	}
}

// C2: get_stock_forecast reuses the standard top_k floor and horizon range
// validators. If those checks regress, ticker-bound calls would silently fall
// through to the build step with bad params.
func TestHandleToolCallGetStockForecastValidatesParams(t *testing.T) {
	_, rpcErr := handleToolCall("get_stock_forecast", json.RawMessage(`{"ticker":"AAPL","top_k":1}`))
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "top_k") {
		t.Fatalf("expected top_k floor error, got %+v", rpcErr)
	}
	_, rpcErr = handleToolCall("get_stock_forecast", json.RawMessage(`{"ticker":"AAPL","horizons":[731]}`))
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "horizons") {
		t.Fatalf("expected horizons range error, got %+v", rpcErr)
	}
}

// C1: alias names dispatch to the same handler as their get_btc_* counterparts.
// Without this test, a future refactor can drop an alias case and clients break silently.
func TestHandleToolCallAliasesDispatchSameAsBTCNames(t *testing.T) {
	setCachedPanelForTest(t, &model.IndicatorPanel{
		Asset:       "btc",
		Date:        "2026-05-09",
		Cycle:       map[string]model.Indicator{"mayer_multiple": {Value: 0.7}},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	})

	cases := []struct {
		alias, legacy, expectKey string
	}{
		{"get_panel", "get_btc_panel", `"date"`},
		{"get_verdict", "get_btc_verdict", `"net_direction"`},
	}
	for _, c := range cases {
		aliasOut, rpcErr := handleToolCall(c.alias, json.RawMessage(`{}`))
		if rpcErr != nil {
			t.Fatalf("%s returned error: %+v", c.alias, rpcErr)
		}
		legacyOut, rpcErr := handleToolCall(c.legacy, json.RawMessage(`{}`))
		if rpcErr != nil {
			t.Fatalf("%s returned error: %+v", c.legacy, rpcErr)
		}
		if !strings.Contains(aliasOut, c.expectKey) {
			t.Fatalf("%s output missing %s: %s", c.alias, c.expectKey, aliasOut)
		}
		if aliasOut != legacyOut {
			t.Fatalf("%s and %s diverged:\nalias=%s\nlegacy=%s", c.alias, c.legacy, aliasOut, legacyOut)
		}
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
		"guanfu://forecast/latest":    "application/json",
		"guanfu://panel/latest/btc":   "application/json",
		"guanfu://panel/latest/qqq":   "application/json",
		"guanfu://verdict/latest/spy": "application/json",
		"guanfu://forecast/latest/gold":"application/json",
		"guanfu://forecast/latest/hs300":"application/json",
		"guanfu://unknown":            "text/plain",
	}
	for uri, want := range cases {
		if got := resourceMimeType(uri); got != want {
			t.Fatalf("resourceMimeType(%q) = %q, want %q", uri, got, want)
		}
	}
}

// C3: parseResourceURI parses guanfu://{kind}/latest[/{asset}] correctly.
func TestParseResourceURI(t *testing.T) {
	cases := []struct {
		uri       string
		wantKind  string
		wantAsset string
		wantOK    bool
	}{
		{"guanfu://panel/latest", "panel", "btc", true},
		{"guanfu://panel/latest/btc", "panel", "btc", true},
		{"guanfu://panel/latest/qqq", "panel", "qqq", true},
		{"guanfu://verdict/latest/gold", "verdict", "gold", true},
		{"guanfu://forecast/latest/hs300", "forecast", "hs300", true},
		{"guanfu://panel/latest/eth", "", "", false}, // unsupported asset
		{"guanfu://knowledge/skill.md", "", "", false},
		{"guanfu://panel/old", "", "", false}, // not "latest"
		{"unknown://foo", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		kind, asset, ok := parseResourceURI(c.uri)
		if ok != c.wantOK || kind != c.wantKind || asset != c.wantAsset {
			t.Errorf("parseResourceURI(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.uri, kind, asset, ok, c.wantKind, c.wantAsset, c.wantOK)
		}
	}
}

// C3: resources/list must include per-asset URIs for all 4 equity assets.
func TestToolsListIncludesPerAssetResources(t *testing.T) {
	resp := handleRequest(&rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "resources/list",
	})
	b, _ := json.Marshal(resp)
	body := string(b)
	for _, suffix := range []string{
		"/latest/qqq", "/latest/spy", "/latest/gold", "/latest/hs300",
	} {
		if !strings.Contains(body, suffix) {
			t.Fatalf("per-asset URI %q not in resources/list: %s", suffix, body)
		}
	}
	// Legacy unsuffixed URIs must still be present.
	for _, suffix := range []string{"/latest", "/skill.md"} {
		if !strings.Contains(body, suffix) {
			t.Fatalf("legacy URI containing %q not in resources/list: %s", suffix, body)
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

func TestValidateForecastArgs(t *testing.T) {
	if rpcErr := validateForecastHorizons([]int{30, 90, 180}); rpcErr != nil {
		t.Fatalf("valid horizons returned error: %+v", rpcErr)
	}
	if rpcErr := validateForecastHorizons([]int{0}); rpcErr == nil {
		t.Fatal("expected non-positive horizon error")
	}
	if rpcErr := validateForecastHorizons([]int{731}); rpcErr == nil {
		t.Fatal("expected max horizon error")
	}
	if minForecastTopK() != 5 {
		t.Fatalf("unexpected min forecast top k: %d", minForecastTopK())
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
