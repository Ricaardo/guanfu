package features

import (
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/store"
)

func TestPutCallRatioExtractor(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]store.PricePoint, 300)
	for i := range points {
		points[i] = store.PricePoint{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  0.8 + float64(i%50)/100,
			Source: "stooq:^PC",
		}
	}
	if err := ps.Save("stooq_putcall", points); err != nil {
		t.Fatal(err)
	}

	ex := PutCallRatioExtractor(ps)
	if ex == nil {
		t.Fatal("expected put/call extractor")
	}
	pricePoints := make([]forecast.Point, 300)
	for i := range pricePoints {
		pricePoints[i] = forecast.Point{
			Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
			Close: 100 + float64(i),
		}
	}
	values, ok := ex(pricePoints, len(pricePoints)-1)
	if !ok {
		t.Fatal("expected put/call features")
	}
	names := map[string]bool{}
	for _, v := range values {
		names[v.Name] = true
		if v.Weight <= 0 {
			t.Fatalf("invalid feature weight: %+v", v)
		}
	}
	for _, name := range []string{"put_call_ratio", "put_call_30d_change", "put_call_252d_percentile"} {
		if !names[name] {
			t.Fatalf("missing %s in %+v", name, values)
		}
	}
}
