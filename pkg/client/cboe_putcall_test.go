package client

import (
	"testing"
	"time"
)

func TestCBOEPutCallSourceInterface(t *testing.T) {
	var s Source = CBOEPutCallSource{}
	if s.Key() != "stooq_putcall" {
		t.Errorf("Key = %q, want stooq_putcall", s.Key())
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName empty")
	}
}

func TestParseCBOEPutCallCSVRow(t *testing.T) {
	p, ok := parseCBOEPutCallCSVRow([]string{"10/04/2019", "2175006", "2289715", "4464721", "1.05"})
	if !ok {
		t.Fatal("expected row to parse")
	}
	if p.Date != "2019-10-04" || p.Close != 1.05 || p.Source != cboePutCallSourceHistorical {
		t.Fatalf("unexpected point: %+v", p)
	}
}

func TestParseCBOEDailyPutCallRatio(t *testing.T) {
	body := `TOTAL PUT/CALL RATIO\",\"value\":\"0.74\"},{\"name\":\"INDEX PUT/CALL RATIO\"`
	got, ok := parseCBOEDailyPutCallRatio(body)
	if !ok || got != 0.74 {
		t.Fatalf("ratio = %v ok=%v, want 0.74 true", got, ok)
	}

	htmlBody := `<td>TOTAL PUT/CALL RATIO</td><td>0.67</td>`
	got, ok = parseCBOEDailyPutCallRatio(htmlBody)
	if !ok || got != 0.67 {
		t.Fatalf("html ratio = %v ok=%v, want 0.67 true", got, ok)
	}
}

func TestCBOEDailyStartDateCapsOldGaps(t *testing.T) {
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	got := cboeDailyStartDate("2020-01-03", now)
	want := time.Date(2025, 3, 17, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("start = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}
