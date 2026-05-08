// Walk-forward backtest for per-asset forecast bundles.
// Validates whether kNN feature bundles generalize out-of-sample.
//
// Usage: go test ./pkg/engine -run TestBacktestBundles -v

package engine

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Ricaardo/guanfu/pkg/forecast"
	"github.com/Ricaardo/guanfu/pkg/forecast/backtest"
	"github.com/Ricaardo/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/guanfu/pkg/store"
)

type yrEntry struct{ yr, n int }

func TestBacktestBundles(t *testing.T) {
	type assetCfg struct {
		name     string
		loadKey  string
		bundleFn func(*store.PriceStore) []forecast.FeatureExtractor
	}

	assets := []assetCfg{
		{"btc", "btc", func(s *store.PriceStore) []forecast.FeatureExtractor {
			return features.CoreExtractors()
		}},
		{"qqq", "qqq", features.EquityExtractors},
		{"spy", "spy", features.EquityExtractors},
		{"gold", "gold", features.GoldExtractors},
		{"hs300", "hs300", features.HS300Extractors},
	}

	for _, a := range assets {
		runAssetBacktest(t, a.name, a.loadKey, 500, a.bundleFn)
	}
}

func runAssetBacktest(t *testing.T, name, loadKey string, minHistory int, bundleFn func(*store.PriceStore) []forecast.FeatureExtractor) {
	s := &store.PriceStore{}
	raw, err := s.Load(loadKey)
	if err != nil || len(raw) < minHistory {
		t.Logf("%-6s SKIP — only %d/%d data points", name, len(raw), minHistory)
		return
	}

	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close}
	}
	points = normalizePoints(points)
	if len(points) < minHistory {
		t.Logf("%-6s SKIP — %d points after dedup", name, len(points))
		return
	}

	extractors := bundleFn(s)
	if len(extractors) == 0 {
		t.Logf("%-6s SKIP — no extractors", name)
		return
	}

	horizons := []int{30, 90, 180}
	startIdx := len(points) / 2
	if startIdx < minHistory {
		startIdx = minHistory
	}

	maxHorizon := 180
	if startIdx+maxHorizon >= len(points) {
		startIdx = len(points) - maxHorizon - 1
		startIdx = (startIdx / 60) * 60
		if startIdx < minHistory {
			t.Logf("%-6s SKIP — insufficient forward room", name)
			return
		}
	}

	result, err := backtest.Run(points, startIdx, 60, extractors, horizons)
	if err != nil {
		t.Logf("%-6s ERROR: %v", name, err)
		return
	}

	t.Logf("\n  %s  (%s)  %d tests, %d extractors",
		name, points[startIdx].Date[:7], result.TotalTests, len(extractors))

	opts := forecast.DefaultOptions()
	opts.Extractors = extractors
	history := points[:startIdx+1]
	fc, err := forecast.Build(history, opts)
	if err == nil {
		fNames := make([]string, len(fc.CurrentFeatures))
		for i, f := range fc.CurrentFeatures {
			fNames[i] = f.Name
		}
		t.Logf("  features: %v", fNames)
	}

	for _, h := range horizons {
		hm := result.ByHorizon[h]
		if hm == nil || hm.SampleCount == 0 {
			continue
		}
		dirHit := hm.DirectionHitRate() * 100
		pit := hm.PITMean()
		crps := hm.CRPSScore()
		calibNote := ""
		switch {
		case pit > 0.62:
			calibNote = " (偏乐观——分布偏窄)"
		case pit < 0.38:
			calibNote = " (偏悲观——分布偏窄)"
		case pit > 0.55:
			calibNote = " (轻微偏乐观)"
		case pit < 0.45:
			calibNote = " (轻微偏悲观)"
		}
		dirNote := ""
		switch {
		case dirHit > 65:
			dirNote = " ★"
		case dirHit < 40:
			dirNote = " ⇣"
		}
		t.Logf("  %3dd: n=%4d  dir_hit=%5.1f%%  PIT=%.2f  CRPS=%.4f%s%s",
			h, hm.SampleCount, dirHit, pit, crps, dirNote, calibNote)
	}

	var yrs []yrEntry
	for yr, ym := range result.ByYear {
		yrs = append(yrs, yrEntry{yr, ym.TotalTests})
	}
	sort.Slice(yrs, func(i, j int) bool { return yrs[i].yr < yrs[j].yr })
	t.Logf("  years: %v", yearStr(yrs))

	t.Logf("  data: %s — %s (%d days)",
		raw[0].Date, raw[len(raw)-1].Date, len(raw))
}

func normalizePoints(points []forecast.Point) []forecast.Point {
	byDate := make(map[string]forecast.Point, len(points))
	for _, p := range points {
		parsed, err := time.Parse("2006-01-02", p.Date)
		if err != nil || p.Close <= 0 {
			continue
		}
		p.Date = parsed.UTC().Format("2006-01-02")
		byDate[p.Date] = p
	}
	out := make([]forecast.Point, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func yearStr(yrList []yrEntry) string {
	s := ""
	for i, yy := range yrList {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d(n=%d)", yy.yr, yy.n)
	}
	return s
}
