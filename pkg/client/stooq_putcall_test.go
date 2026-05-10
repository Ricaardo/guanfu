package client

import (
	"testing"
)

func TestStooqPutCallSourceInterface(t *testing.T) {
	var s Source = StooqPutCallSource{}
	if s.Key() != "stooq_putcall" {
		t.Errorf("Key = %q, want stooq_putcall", s.Key())
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName empty")
	}
}

func TestStooqURLWithAPIKey(t *testing.T) {
	got := stooqURLWithAPIKey("https://stooq.com/q/d/l/?s=^pc&i=d", "abc 123")
	want := "https://stooq.com/q/d/l/?s=^pc&i=d&apikey=abc+123"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}
