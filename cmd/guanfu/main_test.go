package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Ricaardo/guanfu/internal/model"
)

func TestFilterDomainPreservesMetadata(t *testing.T) {
	panel := &model.IndicatorPanel{
		Date: "2026-05-02",
		Snapshot: model.SnapshotData{
			BTCPrice: 100000,
		},
		Cycle: map[string]model.Indicator{
			"phase": {Label: "markup"},
		},
		Flow: map[string]model.Indicator{
			"eth_btc_ratio": {Value: 0.04},
		},
		StaleWarnings: []string{"coinmetrics unavailable"},
		SourceHealth: []model.SourceHealth{
			{Source: "coinmetrics_onchain", Status: "missing"},
		},
	}

	got := filterDomain(panel, "cycle")
	if got.Date != panel.Date || got.Snapshot.BTCPrice != panel.Snapshot.BTCPrice {
		t.Fatalf("metadata not preserved: %+v", got)
	}
	if len(got.StaleWarnings) != 1 || got.StaleWarnings[0] != "coinmetrics unavailable" {
		t.Fatalf("stale warnings not preserved: %+v", got.StaleWarnings)
	}
	if len(got.SourceHealth) != 1 || got.SourceHealth[0].Source != "coinmetrics_onchain" {
		t.Fatalf("source health not preserved: %+v", got.SourceHealth)
	}
	if _, ok := got.Cycle["phase"]; !ok {
		t.Fatal("cycle domain missing")
	}
	if got.Flow != nil {
		t.Fatalf("unexpected flow domain: %+v", got.Flow)
	}
}

func TestPrintHumanPanelPlainOmitsEmojiAndBoxDrawing(t *testing.T) {
	panel := &model.IndicatorPanel{
		Date: "2026-05-02",
		Snapshot: model.SnapshotData{
			BTCPrice:       100000,
			BTCDominance:   0.61,
			FearGreed:      45,
			TotalMarketCap: 3_000_000_000_000,
		},
		Cycle: map[string]model.Indicator{
			"phase": {Label: "markup"},
		},
		StaleWarnings: []string{"coinmetrics unavailable"},
	}

	output := captureStdout(t, func() {
		printHumanPanel(panel, "cycle", true)
	})

	for _, forbidden := range []string{"观复", "🌊", "├", "⚠"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("plain output contains %q:\n%s", forbidden, output)
		}
	}
	for _, want := range []string{"guanfu BTC panel", "Cycle 周期定位", "Data tips:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("plain output missing %q:\n%s", want, output)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	os.Stdout = old

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(b)
}
