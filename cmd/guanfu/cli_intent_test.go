package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ricaardo/guanfu/pkg/claim"
)

// K7 smoke test: runIntent log then list roundtrip through the ledger.
// Can't go through the full CLI main() (it calls os.Exit on invalid
// input); instead exercise the helpers directly.
func TestIntentLogListRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GUANFU_CLAIMS_DIR", dir)

	ledger, err := claim.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.RecordIntent(claim.Intent{
		Asset:        "btc",
		HorizonClass: "5y_hold",
		Thesis:       "测试意图",
	}); err != nil {
		t.Fatalf("RecordIntent: %v", err)
	}

	got, err := ledger.ListIntents(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 intent, got %d", len(got))
	}
	if got[0].Asset != "btc" || got[0].HorizonClass != "5y_hold" {
		t.Errorf("round-trip mismatch: %#v", got[0])
	}
}

func TestParseKVHandlesBothStyles(t *testing.T) {
	got := parseKV([]string{"--asset=btc", "--horizon", "5y_hold", "--thesis", "foo bar"})
	want := map[string]string{
		"asset":   "btc",
		"horizon": "5y_hold",
		"thesis":  "foo bar",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseKV[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestHorizonCadenceKnownClasses(t *testing.T) {
	for _, hc := range []string{"5y_hold", "6m_rebalance", "3m_trade"} {
		if _, n := horizonCadence(hc); n <= 0 {
			t.Errorf("%s should have positive cadence", hc)
		}
	}
	if _, n := horizonCadence("nonsense"); n != 0 {
		t.Errorf("unknown class should return 0, got %d", n)
	}
}

func TestIntentFilesLiveUnderIntentsSubdir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GUANFU_CLAIMS_DIR", dir)

	ledger, err := claim.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.RecordIntent(claim.Intent{
		Asset:        "qqq",
		HorizonClass: "6m_rebalance",
		Thesis:       "t",
	}); err != nil {
		t.Fatal(err)
	}

	// walk intents/ and assert ≥1 json file
	intentsRoot := filepath.Join(dir, "intents")
	var count int
	filepath.Walk(intentsRoot, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".json") {
			count++
		}
		return nil
	})
	if count == 0 {
		t.Error("expected a json under intents/")
	}
}
