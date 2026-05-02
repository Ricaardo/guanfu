package main

import (
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
	}

	got := filterDomain(panel, "cycle")
	if got.Date != panel.Date || got.Snapshot.BTCPrice != panel.Snapshot.BTCPrice {
		t.Fatalf("metadata not preserved: %+v", got)
	}
	if len(got.StaleWarnings) != 1 || got.StaleWarnings[0] != "coinmetrics unavailable" {
		t.Fatalf("stale warnings not preserved: %+v", got.StaleWarnings)
	}
	if _, ok := got.Cycle["phase"]; !ok {
		t.Fatal("cycle domain missing")
	}
	if got.Flow != nil {
		t.Fatalf("unexpected flow domain: %+v", got.Flow)
	}
}
