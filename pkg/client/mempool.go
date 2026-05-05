// mempool.space 客户端 — 拿 BTC 网络数据：哈希率、难度、mempool 拥堵
//
// 全部免费，无需 API key。

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// MempoolData 网络数据汇总
type MempoolData struct {
	HashRate3yEHs       []HashRatePoint `json:"-"`                 // 3 年哈希率序列（用于 hash ribbons 计算）
	HashRateNowEHs      float64         `json:"hash_rate_now_ehs"` // 当前哈希率（EH/s）
	HashRibbons30vs60   string          `json:"hash_ribbons"`      // "上行" / "下行" / "交叉中"
	DifficultyChangePct float64         `json:"difficulty_change_pct"`
	MempoolMB           float64         `json:"mempool_mb"`
	MempoolCount        int             `json:"mempool_count"`
	AsOf                time.Time       `json:"-"`
	Warnings            []string        `json:"-"`
}

// HashRatePoint 单点哈希率
type HashRatePoint struct {
	Timestamp int64
	HashEHs   float64
}

// FetchMempoolData 一次性拉取 mempool.space 三个端点
func FetchMempoolData(ctx context.Context) (*MempoolData, error) {
	out := &MempoolData{}
	httpClient := &http.Client{Timeout: 8 * time.Second}

	// 1. 哈希率（3y 序列）
	if hr, err := fetchHashRate3y(ctx, httpClient); err == nil {
		out.HashRate3yEHs = hr
		if len(hr) > 0 {
			out.HashRateNowEHs = hr[len(hr)-1].HashEHs
			out.HashRibbons30vs60 = computeHashRibbons(hr)
			out.AsOf = time.Unix(hr[len(hr)-1].Timestamp, 0).UTC()
		}
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mempool hash rate fetch failed: %v", err))
	}

	// 2. 难度
	if dc, err := fetchDifficultyAdjust(ctx, httpClient); err == nil {
		out.DifficultyChangePct = dc
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mempool difficulty fetch failed: %v", err))
	}

	// 3. Mempool 拥堵
	if vsize, count, err := fetchMempoolDepth(ctx, httpClient); err == nil {
		out.MempoolMB = float64(vsize) / 1024 / 1024
		out.MempoolCount = count
		if out.AsOf.IsZero() {
			out.AsOf = time.Now().UTC()
		}
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mempool depth fetch failed: %v", err))
	}

	return out, nil
}

func fetchHashRate3y(ctx context.Context, c *http.Client) ([]HashRatePoint, error) {
	body, err := getJSON(ctx, c, "https://mempool.space/api/v1/mining/hashrate/3y")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hashrates []struct {
			Timestamp   int64   `json:"timestamp"`
			AvgHashrate float64 `json:"avgHashrate"`
		} `json:"hashrates"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]HashRatePoint, 0, len(resp.Hashrates))
	for _, h := range resp.Hashrates {
		out = append(out, HashRatePoint{
			Timestamp: h.Timestamp,
			HashEHs:   h.AvgHashrate / 1e18, // hash/s → EH/s
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

func fetchDifficultyAdjust(ctx context.Context, c *http.Client) (float64, error) {
	body, err := getJSON(ctx, c, "https://mempool.space/api/v1/difficulty-adjustment")
	if err != nil {
		return 0, err
	}
	var resp struct {
		PreviousRetarget float64 `json:"previousRetarget"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	return resp.PreviousRetarget, nil
}

func fetchMempoolDepth(ctx context.Context, c *http.Client) (vsize int64, count int, err error) {
	body, err := getJSON(ctx, c, "https://mempool.space/api/mempool")
	if err != nil {
		return 0, 0, err
	}
	var resp struct {
		Count int   `json:"count"`
		Vsize int64 `json:"vsize"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, err
	}
	return resp.Vsize, resp.Count, nil
}

// computeHashRibbons 30d MA vs 60d MA of daily hash rate.
//
// Uses the last 180 days of data for stable moving averages (3 × 60d),
// not just 60 points, to avoid short-term noise causing false signals.
// Returns "上行" (30 > 60 : miner expansion) / "下行" (30 < 60 : miner capitulation) / "交叉中".
func computeHashRibbons(hr []HashRatePoint) string {
	const (
		ma30Window = 30
		ma60Window = 60
		minData    = 180 // need at least 3 × 60d for a stable 60d MA
	)

	if len(hr) < minData {
		// Fallback: use what we have, but require at least 60 points
		if len(hr) < ma60Window {
			return "n/a"
		}
	}

	// Work with the last N points, up to 180
	tail := hr
	if len(hr) > minData {
		tail = hr[len(hr)-minData:]
	}

	// 30d MA: average of last 30 points
	ma30 := 0.0
	start30 := len(tail) - ma30Window
	for _, p := range tail[start30:] {
		ma30 += p.HashEHs
	}
	ma30 /= float64(ma30Window)

	// 60d MA: average of all available tail points (up to 180, min 60)
	ma60 := 0.0
	for _, p := range tail {
		ma60 += p.HashEHs
	}
	ma60 /= float64(len(tail))

	diff := (ma30 - ma60) / ma60
	switch {
	case diff > 0.02:
		return "上行（矿工扩张）"
	case diff < -0.02:
		return "下行（矿工投降信号 ⚠）"
	default:
		return "交叉中"
	}
}

// getJSON 简单 GET helper
func getJSON(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
