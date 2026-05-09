package client

import "testing"

func TestCoinbaseBTCSourceInterface(t *testing.T) {
	var s Source = CoinbaseBTCSource{}
	if s.Key() != "coinbase_btc" {
		t.Errorf("Key = %q, want coinbase_btc", s.Key())
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName empty")
	}
}
