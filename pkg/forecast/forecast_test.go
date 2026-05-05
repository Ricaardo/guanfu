package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

func TestBuildForecastFromSyntheticHistory(t *testing.T) {
	points := syntheticPoints(3200)

	fc, err := Build(points, DefaultOptions())
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if fc.Method != "historical_analogue_knn_v1" {
		t.Fatalf("unexpected method: %s", fc.Method)
	}
	if fc.Coverage.SelectedAnalogs < minSelectedAnalogs {
		t.Fatalf("selected analogs = %d, want >= %d", fc.Coverage.SelectedAnalogs, minSelectedAnalogs)
	}
	if len(fc.Horizons) != 3 {
		t.Fatalf("horizons = %d, want 3", len(fc.Horizons))
	}
	for _, h := range fc.Horizons {
		sum := h.ProbabilityUpsideContinuation + h.ProbabilityRange + h.ProbabilityDownsidePressure
		if math.Abs(sum-1) > 0.0001 {
			t.Fatalf("scenario probabilities for %dd sum to %.6f", h.Days, sum)
		}
		if h.SampleSize == 0 || h.MedianPrice <= 0 || h.P10Price <= 0 || h.P90Price <= 0 {
			t.Fatalf("invalid horizon summary: %+v", h)
		}
	}
	if len(fc.CurrentFeatures) < minSharedFeatures {
		t.Fatalf("current features = %d, want >= %d", len(fc.CurrentFeatures), minSharedFeatures)
	}
}

func TestParseHorizonsNormalizesDays(t *testing.T) {
	got, err := ParseHorizons("180d,30,90,30")
	if err != nil {
		t.Fatalf("ParseHorizons returned error: %v", err)
	}
	want := []int{30, 90, 180}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("horizons = %v, want %v", got, want)
		}
	}
}

func TestPointsFromSnapshotReconstructsOldestFirstDates(t *testing.T) {
	snap := &model.MarketSnapshot{
		Date:         time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
		BTCPriceAsOf: "2026-05-03",
		BTCPriceHistory: []decimal.Decimal{
			decimal.NewFromInt(103),
			decimal.NewFromInt(102),
			decimal.NewFromInt(101),
		},
	}

	points, err := PointsFromSnapshot(snap)
	if err != nil {
		t.Fatalf("PointsFromSnapshot returned error: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("len = %d, want 3", len(points))
	}
	if points[0].Date != "2026-05-01" || points[0].Close != 101 {
		t.Fatalf("oldest point mismatch: %+v", points[0])
	}
	if points[2].Date != "2026-05-03" || points[2].Close != 103 {
		t.Fatalf("latest point mismatch: %+v", points[2])
	}
}

func TestBuildRejectsShortHistory(t *testing.T) {
	_, err := Build(syntheticPoints(250), DefaultOptions())
	if err == nil {
		t.Fatal("expected short history error")
	}
}

func syntheticPoints(n int) []Point {
	start := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]Point, 0, n)
	for i := 0; i < n; i++ {
		x := float64(i)
		price := 250 * math.Exp(0.0018*x+0.28*math.Sin(x/95)+0.08*math.Sin(x/17))
		points = append(points, Point{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  price,
			Source: "test",
		})
	}
	return points
}
