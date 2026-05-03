package client

import (
	"encoding/json"
	"testing"
)

func TestParseSoSoETFRowsSupportsLegacyArray(t *testing.T) {
	rows, err := parseSoSoETFRows(json.RawMessage(`[
		{"date":"2026-05-01","totalNetInflow":100,"totalNetAssets":200,"cumNetInflow":300}
	]`))
	if err != nil {
		t.Fatalf("parse legacy rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Date != "2026-05-01" || rows[0].TotalNetInflow != 100 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestParseSoSoETFRowsSupportsWrappedList(t *testing.T) {
	rows, err := parseSoSoETFRows(json.RawMessage(`{
		"list":[{"date":"2026-05-02","totalNetInflow":150,"totalNetAssets":250,"cumNetInflow":350}],
		"total":1
	}`))
	if err != nil {
		t.Fatalf("parse wrapped rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Date != "2026-05-02" || rows[0].TotalNetAssets != 250 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}
