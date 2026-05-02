package client

import (
	"testing"
	"time"

	"github.com/fengenci/guanfu/internal/model"

	"github.com/shopspring/decimal"
)

func TestUsableCachedSnapshotValidatesSchemaAndFreshness(t *testing.T) {
	valid := &model.MarketSnapshot{
		Date:                  time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		SnapshotSchemaVersion: model.CurrentMarketSnapshotSchemaVersion,
		BTCPrice:              decimal.NewFromInt(100000),
		BTCPriceAsOf:          "2026-05-02",
		BTCPriceHistory:       make([]decimal.Decimal, btcHistoryMinFreshDays),
	}

	if ok, reason := usableCachedSnapshot(valid); !ok {
		t.Fatalf("expected valid cached snapshot, got reason %q", reason)
	}

	oldSchema := *valid
	oldSchema.SnapshotSchemaVersion = model.CurrentMarketSnapshotSchemaVersion - 1
	if ok, _ := usableCachedSnapshot(&oldSchema); ok {
		t.Fatal("expected old schema cache to be rejected")
	}

	missingAsOf := *valid
	missingAsOf.BTCPriceAsOf = ""
	if ok, _ := usableCachedSnapshot(&missingAsOf); ok {
		t.Fatal("expected cache without BTC as-of timestamp to be rejected")
	}

	shortHistory := *valid
	shortHistory.BTCPriceHistory = make([]decimal.Decimal, btcHistoryMinFreshDays-1)
	if ok, _ := usableCachedSnapshot(&shortHistory); ok {
		t.Fatal("expected cache with short BTC history to be rejected")
	}
}
