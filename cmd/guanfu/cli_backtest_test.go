package main

import "testing"

func TestNormalizeBacktestAssetRejectsUnsupportedAsset(t *testing.T) {
	for _, asset := range []string{"", "btc", "QQQ", "spy", "gold"} {
		if got, ok := normalizeBacktestAsset(asset); !ok || got == "" {
			t.Fatalf("expected supported asset %q to normalize, got %q ok=%v", asset, got, ok)
		}
	}

	removedAsset := "hs" + "300"
	if got, ok := normalizeBacktestAsset(removedAsset); ok || got != removedAsset {
		t.Fatalf("removed asset should be rejected, got %q ok=%v", got, ok)
	}
}
