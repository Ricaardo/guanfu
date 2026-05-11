package client

import (
	"context"
	"encoding/csv"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/guanfu/pkg/store"
)

const (
	cboePutCallStorageKey       = "stooq_putcall"
	cboePutCallDailyURL         = "https://www.cboe.com/markets/us/options/market-statistics/daily"
	cboePutCallRecentCSVURL     = "https://cdn.cboe.com/resources/options/volume_and_call_put_ratios/totalpc.csv"
	cboePutCallArchiveCSVURL    = "https://cdn.cboe.com/resources/options/volume_and_call_put_ratios/totalpcarchive.csv"
	cboePutCallRecentStart      = "2019-10-07"
	cboePutCallRecentLookback   = 420
	cboePutCallWorkers          = 12
	cboePutCallRequestTimeout   = 20 * time.Second
	cboePutCallSourceHistorical = "cboe:volume_and_call_put_ratios"
	cboePutCallSourceDaily      = "cboe:daily_market_statistics"
)

var (
	cboeDailyEscapedRatioRE = regexp.MustCompile(`TOTAL PUT/CALL RATIO\\",\\"value\\":\\"([0-9]+(?:\.[0-9]+)?)\\"`)
	cboeDailyTextRatioRE    = regexp.MustCompile(`TOTAL PUT/CALL RATIO[^0-9]{0,120}([0-9]+(?:\.[0-9]+)?)`)
	cboeHTMLTagRE           = regexp.MustCompile(`<[^>]+>`)
)

// CBOEPutCallSource fetches CBOE total Put/Call ratio without an API key.
//
// The storage key remains "stooq_putcall" for compatibility with existing
// forecast features and local archives. New data is sourced directly from
// CBOE official historical CSVs and Daily Market Statistics pages.
type CBOEPutCallSource struct{}

func (CBOEPutCallSource) Key() string { return cboePutCallStorageKey }
func (CBOEPutCallSource) DisplayName() string {
	return "stooq_putcall (CBOE official total P/C, no key)"
}

// Refresh uses CBOE historical CSVs for old data and CBOE Daily Market
// Statistics for recent/current data. CBOE does not expose a documented bulk
// post-2019 CSV, so first-run refresh limits page scraping to a recent window
// that is long enough for the 252-observation percentile feature.
func (s CBOEPutCallSource) Refresh(ctx context.Context, ps *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(ps, s.Key())
	if !stale {
		return freshSkipResult(s.Key(), s.DisplayName(), lastDate, ps), nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}

	points := make([]store.PricePoint, 0, 6000)
	if lastDate == "" || dateBefore(lastDate, "2019-10-04") {
		hist, err := fetchCBOEPutCallCSVs(ctx)
		if err != nil {
			return nil, err
		}
		points = append(points, hist...)
	}

	dailyStart := cboeDailyStartDate(lastDate, time.Now())
	recent, err := fetchCBOEDailyPutCallRange(ctx, dailyStart, time.Now())
	if err != nil {
		return nil, err
	}
	points = append(points, recent...)

	if len(points) == 0 {
		count, _ := ps.Count(s.Key())
		return &RefreshResult{
			Key:         s.Key(),
			DisplayName: s.DisplayName(),
			Mode:        "skip",
			SkipReason:  "no_new_data",
			Stale:       stale,
			Action:      "ignore",
			Total:       count,
			LastDate:    lastDate,
		}, nil
	}

	var added int
	if lastDate == "" {
		if err := ps.Save(s.Key(), points); err != nil {
			return nil, err
		}
		added = len(store.NormalizePricePoints(points))
	} else {
		before, _ := ps.Count(s.Key())
		if err := ps.Append(s.Key(), points); err != nil {
			return nil, err
		}
		after, _ := ps.Count(s.Key())
		added = after - before
	}

	total, _ := ps.Count(s.Key())
	last, _ := ps.LastDate(s.Key())
	return &RefreshResult{
		Key:         s.Key(),
		DisplayName: s.DisplayName(),
		Mode:        mode,
		Added:       added,
		Total:       total,
		LastDate:    last,
	}, nil
}

func fetchCBOEPutCallCSVs(ctx context.Context) ([]store.PricePoint, error) {
	urls := []string{cboePutCallArchiveCSVURL, cboePutCallRecentCSVURL}
	var out []store.PricePoint
	for _, rawURL := range urls {
		points, err := fetchCBOEPutCallCSV(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		out = append(out, points...)
	}
	return store.NormalizePricePoints(out), nil
}

func fetchCBOEPutCallCSV(ctx context.Context, rawURL string) ([]store.PricePoint, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "guanfu/1.0 (CBOE put/call)")

	c := &http.Client{Timeout: cboePutCallRequestTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cboe put/call csv fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cboe put/call csv %s %d: %s", rawURL, resp.StatusCode, truncate(string(body), 200))
	}

	r := csv.NewReader(resp.Body)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true

	var out []store.PricePoint
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cboe put/call csv parse %s: %w", rawURL, err)
		}
		p, ok := parseCBOEPutCallCSVRow(row)
		if ok {
			out = append(out, p)
		}
	}
	return store.NormalizePricePoints(out), nil
}

func parseCBOEPutCallCSVRow(row []string) (store.PricePoint, bool) {
	if len(row) < 5 {
		return store.PricePoint{}, false
	}
	dateRaw := cleanCBOECSVField(row[0])
	t, err := time.Parse("1/2/2006", dateRaw)
	if err != nil {
		return store.PricePoint{}, false
	}
	ratioRaw := ""
	for i := len(row) - 1; i >= 1; i-- {
		ratioRaw = cleanCBOECSVField(row[i])
		if ratioRaw != "" {
			break
		}
	}
	ratio, err := strconv.ParseFloat(strings.ReplaceAll(ratioRaw, ",", ""), 64)
	if err != nil || ratio <= 0 {
		return store.PricePoint{}, false
	}
	return store.PricePoint{
		Date:   t.Format("2006-01-02"),
		Close:  ratio,
		Source: cboePutCallSourceHistorical,
	}, true
}

func cleanCBOECSVField(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.Trim(s, "\"")
	return strings.TrimSpace(s)
}

func cboeDailyStartDate(lastDate string, now time.Time) time.Time {
	recentStart := dateOnly(now).AddDate(0, 0, -cboePutCallRecentLookback)
	minRecent, _ := time.Parse("2006-01-02", cboePutCallRecentStart)
	if recentStart.Before(minRecent) {
		recentStart = minRecent
	}
	if lastDate == "" {
		return recentStart
	}
	last, err := time.Parse("2006-01-02", lastDate)
	if err != nil {
		return recentStart
	}
	next := last.AddDate(0, 0, 1)
	if next.Before(recentStart) {
		return recentStart
	}
	return next
}

func fetchCBOEDailyPutCallRange(ctx context.Context, start, end time.Time) ([]store.PricePoint, error) {
	start = dateOnly(start)
	end = dateOnly(end)
	if start.After(end) {
		return nil, nil
	}

	dates := make([]string, 0, int(end.Sub(start).Hours()/24)+1)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dates = append(dates, d.Format("2006-01-02"))
	}
	if len(dates) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := &http.Client{Timeout: cboePutCallRequestTimeout}
	jobs := make(chan string)
	results := make(chan store.PricePoint, len(dates))
	errs := make(chan error, len(dates))

	var wg sync.WaitGroup
	workers := cboePutCallWorkers
	if len(dates) < workers {
		workers = len(dates)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for date := range jobs {
				point, ok, err := fetchCBOEDailyPutCall(ctx, c, date)
				if err != nil {
					errs <- err
					cancel()
					continue
				}
				if ok {
					results <- point
				}
			}
		}()
	}

sendLoop:
	for _, date := range dates {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- date:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	close(errs)

	if len(errs) > 0 {
		return nil, <-errs
	}

	out := make([]store.PricePoint, 0, len(results))
	for p := range results {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return store.NormalizePricePoints(out), nil
}

func fetchCBOEDailyPutCall(ctx context.Context, c *http.Client, date string) (store.PricePoint, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", cboePutCallDailyURL+"?dt="+date, nil)
	if err != nil {
		return store.PricePoint{}, false, err
	}
	req.Header.Set("User-Agent", "guanfu/1.0 (CBOE put/call)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return store.PricePoint{}, false, ctx.Err()
		}
		return store.PricePoint{}, false, fmt.Errorf("cboe daily put/call fetch %s: %w", date, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return store.PricePoint{}, false, fmt.Errorf("cboe daily put/call %s %d: %s", date, resp.StatusCode, truncate(string(body), 200))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return store.PricePoint{}, false, err
	}
	ratio, ok := parseCBOEDailyPutCallRatio(string(body))
	if !ok {
		return store.PricePoint{}, false, nil
	}
	return store.PricePoint{
		Date:   date,
		Close:  ratio,
		Source: cboePutCallSourceDaily,
	}, true, nil
}

func parseCBOEDailyPutCallRatio(body string) (float64, bool) {
	if m := cboeDailyEscapedRatioRE.FindStringSubmatch(body); len(m) == 2 {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil && v > 0
	}
	text := html.UnescapeString(body)
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = cboeHTMLTagRE.ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")
	if m := cboeDailyTextRatioRE.FindStringSubmatch(text); len(m) == 2 {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil && v > 0
	}
	return 0, false
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func dateBefore(a, b string) bool {
	ta, errA := time.Parse("2006-01-02", a)
	tb, errB := time.Parse("2006-01-02", b)
	return errA == nil && errB == nil && ta.Before(tb)
}
