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
