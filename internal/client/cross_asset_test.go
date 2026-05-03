package client

import (
	"reflect"
	"testing"
	"time"
)

func TestYahooClosesNewestFirst(t *testing.T) {
	a := 10.0
	b := 20.0
	c := 30.0

	got := yahooClosesNewestFirst([]*float64{&a, &b, nil, &c})
	want := []float64{30, 0, 20, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected newest-first Yahoo history %v, got %v", want, got)
	}
}

func TestLatestYahooCloseUsesNewestAvailableTimestamp(t *testing.T) {
	old := 10.0
	latest := 30.0
	timestamps := []int64{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Unix(),
		time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).Unix(),
	}

	price, asOf := latestYahooClose([]*float64{&old, &latest, nil}, timestamps)
	if price != latest {
		t.Fatalf("expected latest available close %.2f, got %.2f", latest, price)
	}
	if asOf != "2026-01-02" {
		t.Fatalf("expected fallback as_of 2026-01-02, got %q", asOf)
	}
}

func TestNormalizeFutuKLNewestFirst(t *testing.T) {
	kl := []FutuKLPoint{
		{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Close: 10},
		{Time: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), Close: 30},
		{Time: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Close: 20},
	}

	normalizeFutuKLNewestFirst(kl)
	got := klToFloat64(kl)
	want := []float64{30, 20, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected Futu history newest-first %v, got %v", want, got)
	}
}
