package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
