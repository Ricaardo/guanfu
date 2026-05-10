// Intent subcommand — user-declared investment theses (K7) and drift
// review (K8). Stored in the same ledger as tool-emitted Claims but
// under intents/ prefix.
//
// Usage:
//
//	guanfu intent log --asset btc --horizon 5y_hold --thesis "M2 扩张长期积累"
//	guanfu intent list [--asset btc] [--since 2026-01-01]
//	guanfu intent review       # K8 drift check
//
// Intents are deliberately thin: 1 asset / 1 horizon class / 1 thesis
// line. Complex multi-leg strategies belong in a separate system; we
// just want the user's self-declared discipline checkpoint.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/guanfu/pkg/claim"
	"github.com/Ricaardo/guanfu/pkg/client"
	"github.com/Ricaardo/guanfu/pkg/store"
)

const intentUsage = `usage: guanfu intent <log|list|review> [flags]

  log      --asset X --horizon 5y_hold|6m_rebalance|3m_trade --thesis "..."
           [--note "..."]
  list     [--asset X] [--since YYYY-MM-DD]
  review   [--asset X] [--lookback-days N]   # K8 drift check (N default 90)
`

func runIntent(args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, intentUsage)
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "log":
		runIntentLog(rest)
	case "list":
		runIntentList(rest)
	case "review":
		runIntentReview(rest)
	default:
		fmt.Fprintf(os.Stderr, "guanfu intent: unknown subcommand %q\n%s", sub, intentUsage)
		os.Exit(1)
	}
}

func runIntentLog(args []string) {
	flags := parseKV(args)
	asset := strings.ToLower(flags["asset"])
	horizon := strings.ToLower(flags["horizon"])
	thesis := flags["thesis"]
	note := flags["note"]

	if asset == "" {
		fmt.Fprintln(os.Stderr, "intent log: --asset required")
		os.Exit(1)
	}
	if !isValidHorizonClass(horizon) {
		fmt.Fprintf(os.Stderr, "intent log: --horizon must be one of 5y_hold / 6m_rebalance / 3m_trade (got %q)\n", horizon)
		os.Exit(1)
	}
	if thesis == "" {
		fmt.Fprintln(os.Stderr, "intent log: --thesis required (one sentence summary)")
		os.Exit(1)
	}

	ledger, err := claim.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "intent: open ledger failed: %v\n", err)
		os.Exit(1)
	}
	it := claim.Intent{
		Asset:               asset,
		HorizonClass:        horizon,
		Thesis:              thesis,
		CurrentPositionNote: note,
	}
	path, err := ledger.RecordIntent(it)
	if err != nil {
		fmt.Fprintf(os.Stderr, "intent log: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ intent logged: %s\n  asset=%s horizon=%s\n  thesis: %s\n",
		path, asset, horizon, thesis)
}

func runIntentList(args []string) {
	flags := parseKV(args)
	assetFilter := strings.ToLower(flags["asset"])
	var sinceT time.Time
	if s := flags["since"]; s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --since: %v\n", err)
			os.Exit(1)
		}
		sinceT = t
	}

	ledger, err := claim.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "intent: open ledger failed: %v\n", err)
		os.Exit(1)
	}
	intents, err := ledger.ListIntents(func(it claim.Intent) bool {
		if assetFilter != "" && it.Asset != assetFilter {
			return false
		}
		if !sinceT.IsZero() && it.AsOf.Before(sinceT) {
			return false
		}
		return true
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "intent list: %v\n", err)
		os.Exit(1)
	}
	if len(intents) == 0 {
		fmt.Println("no intents recorded (use `guanfu intent log ...`)")
		return
	}
	for _, it := range intents {
		fmt.Printf("%s  %s/%s\n  %s\n",
			it.AsOf.Format("2006-01-02"), strings.ToUpper(it.Asset), it.HorizonClass, it.Thesis)
		if it.CurrentPositionNote != "" {
			fmt.Printf("  pos: %s\n", it.CurrentPositionNote)
		}
	}
}

// runIntentReview (K8) is a drift check: for each Intent in the lookback,
// count how many days the declared trigger conditions were met based on
// current panel data — a rough proxy for "you said you'd act, did you
// notice the opportunity?". It does NOT judge execution itself (we have
// no trade log); it surfaces the observational gap.
//
// Minimum viable implementation: report intents, their age, and a
// "needs manual review" flag for intents older than horizon_class's
// natural review period. Full opportunity-window counting requires
// per-day historical panel reconstruction, which is a Wave-3 follow-up.
func runIntentReview(args []string) {
	flags := parseKV(args)
	assetFilter := strings.ToLower(flags["asset"])
	lookback := 90
	if n := flags["lookback-days"]; n != "" {
		fmt.Sscanf(n, "%d", &lookback)
	}
	if lookback <= 0 {
		lookback = 90
	}

	ledger, err := claim.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "intent: open ledger failed: %v\n", err)
		os.Exit(1)
	}
	cutoff := time.Now().AddDate(0, 0, -lookback)
	intents, err := ledger.ListIntents(func(it claim.Intent) bool {
		if assetFilter != "" && it.Asset != assetFilter {
			return false
		}
		return !it.AsOf.Before(cutoff)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "intent review: %v\n", err)
		os.Exit(1)
	}
	if len(intents) == 0 {
		fmt.Printf("no intents in last %d days\n", lookback)
		return
	}

	fmt.Printf("Intent review  (lookback %dd, %d intents)\n\n", lookback, len(intents))
	fmt.Println("  This is a first-pass drift check. Opportunity-window counting")
	fmt.Println("  against historical panels is a follow-up; for now we surface")
	fmt.Println("  age + natural review cadence to prompt manual reflection.")
	fmt.Println()

	for _, it := range intents {
		ageDays := int(time.Since(it.AsOf).Hours() / 24)
		cadence, natural := horizonCadence(it.HorizonClass)
		overdue := ageDays > natural && natural > 0
		flag := "  "
		if overdue {
			flag = "⚠ "
		}
		fmt.Printf("%s[%s] %s/%s  (age %dd, natural review every %s)\n",
			flag, it.AsOf.Format("2006-01-02"),
			strings.ToUpper(it.Asset), it.HorizonClass,
			ageDays, cadence)
		fmt.Printf("     thesis: %s\n", it.Thesis)
		if len(it.TriggerBuy) > 0 || len(it.TriggerSell) > 0 {
			fmt.Printf("     triggers: buy=%d / sell=%d  (opportunity-window check: TODO Wave 3)\n",
				len(it.TriggerBuy), len(it.TriggerSell))
		}
		if overdue {
			fmt.Printf("     → past natural review window; re-read and decide if thesis still holds\n")
		}
		fmt.Println()
	}

	// Price drift hint: for each asset with an intent, show current price
	// so the user can eyeball "did the market move against my thesis?".
	// No verdict, no judgment — just data.
	seenAssets := map[string]bool{}
	for _, it := range intents {
		if seenAssets[it.Asset] {
			continue
		}
		seenAssets[it.Asset] = true
		if p := peekAssetPrice(it.Asset); p > 0 {
			fmt.Printf("  %s current: $%.2f\n", strings.ToUpper(it.Asset), p)
		}
	}
}

// horizonCadence returns a human label and day count for how often an
// intent of this class should be reviewed. "Natural" = a window past
// which the user should re-read the thesis.
func horizonCadence(hc string) (string, int) {
	switch hc {
	case "5y_hold":
		return "6 months", 180
	case "6m_rebalance":
		return "1 month", 30
	case "3m_trade":
		return "1 week", 7
	}
	return "undefined", 0
}

func isValidHorizonClass(hc string) bool {
	_, n := horizonCadence(hc)
	return n > 0
}

// peekAssetPrice reads the latest price from the PriceStore without
// triggering refresh. Returns 0 if unavailable — the review output
// degrades gracefully to "no price line".
func peekAssetPrice(asset string) float64 {
	s := &store.PriceStore{}
	key := asset
	if _, ok := map[string]bool{"btc": true, "qqq": true, "spy": true, "gold": true}[asset]; !ok {
		// Assume stock_X namespace.
		key = client.StockKey(asset)
	}
	p, ok := s.Latest(key)
	if !ok {
		return 0
	}
	return p.Close
}

// parseKV extracts --key=value / --key value from a flag slice.
func parseKV(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") && !strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			out[a[:eq]] = a[eq+1:]
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out[a] = args[i+1]
			i++
		} else {
			out[a] = "true"
		}
	}
	return out
}

// Unused-import guard so `context` and `json` don't yell if I later drop
// their use. Remove freely; kept for intended review expansion.
var _ = context.Background
var _ = json.Marshal
