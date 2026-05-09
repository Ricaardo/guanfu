// Package portfolio reads the user's self-declared holdings + preferences
// + behavioral guardrails from ~/.guanfu/portfolio.json. It drives
// portfolio-aware verdict output (Track L, L3) and behavioral checks
// in SKILL-rendered responses.
//
// Opt-in: if the file doesn't exist, Load returns (nil, nil) and callers
// fall back to portfolio-agnostic behavior. That keeps the no-portfolio
// path identical to v2.
//
// Format is JSON (not YAML as the roadmap originally drafted) because
// guanfu has no yaml dependency and portfolio data is simple enough
// that JSON is acceptable ergonomic-wise. Example:
//
//	{
//	  "schema_version": 1,
//	  "holdings": {
//	    "btc":  {"amount": 0.35, "cost_basis_usd": 42000, "acquired": "2023-06"},
//	    "qqq":  {"shares": 50,   "cost_basis_usd": 380},
//	    "cash": {"usd": 30000, "cny": 100000}
//	  },
//	  "preferences": {
//	    "horizon_years": 5,
//	    "risk_budget": "moderate",
//	    "home_currency": "CNY",
//	    "ceiling_pct": {"btc": 25, "equity": 60, "gold": 15}
//	  },
//	  "behavior": {
//	    "cooldown_hours": 4,
//	    "fomo_threshold_pct": 20,
//	    "panic_threshold_pct": 20
//	  }
//	}

package portfolio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const SchemaVersion = 1

// Portfolio is the user's opt-in self-declared context.
type Portfolio struct {
	SchemaVersion int                 `json:"schema_version"`
	Holdings      map[string]Holding  `json:"holdings"`
	Preferences   Preferences         `json:"preferences"`
	Behavior      Behavior            `json:"behavior"`
}

// Holding is a flexible record — different assets use different fields.
// For crypto: amount + cost_basis_usd. For stocks: shares + cost_basis_usd.
// For cash: usd / cny / etc. Not all fields are used for all assets.
type Holding struct {
	Amount       float64 `json:"amount,omitempty"`         // BTC / GLD oz / etc.
	Shares       float64 `json:"shares,omitempty"`         // stocks / ETFs
	USD          float64 `json:"usd,omitempty"`            // cash
	CNY          float64 `json:"cny,omitempty"`            // cash
	CostBasisUSD float64 `json:"cost_basis_usd,omitempty"` // per unit
	Acquired     string  `json:"acquired,omitempty"`       // YYYY-MM
}

type Preferences struct {
	HorizonYears int                `json:"horizon_years"`
	RiskBudget   string             `json:"risk_budget"`   // conservative / moderate / aggressive
	HomeCurrency string             `json:"home_currency"` // CNY / USD / JPY ...
	CeilingPct   map[string]float64 `json:"ceiling_pct"`   // per-asset max %
}

type Behavior struct {
	CooldownHours     int     `json:"cooldown_hours"`
	FOMOThresholdPct  float64 `json:"fomo_threshold_pct"`
	PanicThresholdPct float64 `json:"panic_threshold_pct"`
}

// DefaultPath returns the expected portfolio.json location:
// $GUANFU_PORTFOLIO (if set) or ~/.guanfu/portfolio.json.
func DefaultPath() string {
	if p := os.Getenv("GUANFU_PORTFOLIO"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "" // caller handles the empty-path-means-skip case
	}
	return filepath.Join(home, ".guanfu", "portfolio.json")
}

// Load reads and validates the portfolio file. Returns (nil, nil) when
// the file does not exist — that's the "no portfolio configured" path
// and is not an error. Returns an error only for malformed JSON or
// schema issues.
func Load(path string) (*Portfolio, error) {
	if path == "" {
		path = DefaultPath()
	}
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read portfolio: %w", err)
	}
	var p Portfolio
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse portfolio: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate enforces minimum schema requirements and normalizes keys.
// Not strict: unknown fields pass through (future compatibility),
// but schema version must be readable.
func (p *Portfolio) Validate() error {
	if p.SchemaVersion == 0 {
		// Zero-value = no declaration; assume current version for forward
		// compatibility but warn via ValidationNotes if we add any.
		p.SchemaVersion = SchemaVersion
	}
	if p.SchemaVersion > SchemaVersion {
		return fmt.Errorf("portfolio schema_version %d newer than supported %d; upgrade guanfu",
			p.SchemaVersion, SchemaVersion)
	}
	// Normalize asset keys to lowercase.
	if p.Holdings != nil {
		norm := make(map[string]Holding, len(p.Holdings))
		for k, v := range p.Holdings {
			norm[strings.ToLower(k)] = v
		}
		p.Holdings = norm
	}
	if p.Preferences.CeilingPct != nil {
		norm := make(map[string]float64, len(p.Preferences.CeilingPct))
		for k, v := range p.Preferences.CeilingPct {
			norm[strings.ToLower(k)] = v
		}
		p.Preferences.CeilingPct = norm
	}
	if b := strings.ToLower(p.Preferences.RiskBudget); b != "" {
		switch b {
		case "conservative", "moderate", "aggressive":
			p.Preferences.RiskBudget = b
		default:
			return fmt.Errorf("invalid risk_budget %q (want conservative/moderate/aggressive)", b)
		}
	}
	return nil
}

// HoldingFor returns (holding, ok) for an asset key. Case-insensitive.
func (p *Portfolio) HoldingFor(asset string) (Holding, bool) {
	if p == nil || p.Holdings == nil {
		return Holding{}, false
	}
	h, ok := p.Holdings[strings.ToLower(asset)]
	return h, ok
}

// CeilingFor returns the user-declared max % for an asset. 0 means
// unlimited / unspecified.
func (p *Portfolio) CeilingFor(asset string) float64 {
	if p == nil {
		return 0
	}
	return p.Preferences.CeilingPct[strings.ToLower(asset)]
}

// PositionValueUSD estimates the holding's USD value given a current
// price. For crypto uses Amount * price; for stocks uses Shares * price;
// for cash ignores the price arg and returns USD (+ CNY converted at
// a placeholder 1 USD = 7 CNY if specified).
// Returns 0 if we can't compute — callers fall back to "position unknown".
func (p *Portfolio) PositionValueUSD(asset string, currentPrice float64) float64 {
	h, ok := p.HoldingFor(asset)
	if !ok {
		return 0
	}
	switch {
	case h.Amount > 0 && currentPrice > 0:
		return h.Amount * currentPrice
	case h.Shares > 0 && currentPrice > 0:
		return h.Shares * currentPrice
	case h.USD > 0 || h.CNY > 0:
		// Cash — return USD + rough CNY conversion as a proxy. Real FX
		// conversion is L7 (roadmap); this is a good-enough fallback.
		return h.USD + h.CNY/7.0
	}
	return 0
}

// TotalValueUSD sums all holdings at given asset prices. Unknown
// holdings (no price provided) contribute cash-only fields.
// prices is a lowercase-asset map; missing price = skip that sleeve.
func (p *Portfolio) TotalValueUSD(prices map[string]float64) float64 {
	if p == nil {
		return 0
	}
	total := 0.0
	for asset := range p.Holdings {
		total += p.PositionValueUSD(asset, prices[asset])
	}
	return total
}

// CurrencyPairUSDRate returns how many units of the given currency per 1
// USD, using the PriceStore's hs300_cny rate when available, and sensible
// fallbacks for JPY/EUR/GBP otherwise. This is intentionally best-effort —
// we'd rather show a slightly-stale conversion than block the output. The
// caller passes in a PriceStore-resolved USD/CNY rate (or 0 to use the
// fallback); other currencies use ECB-rough fallbacks.
//
// Returns 0 on unknown currency. USD→USD returns 1.
func CurrencyPairUSDRate(currency string, cnyRate float64) float64 {
	c := strings.ToUpper(strings.TrimSpace(currency))
	switch c {
	case "USD", "":
		return 1.0
	case "CNY":
		if cnyRate > 0 {
			return cnyRate
		}
		return 7.2 // ECB fallback rounded to 1 dp
	case "JPY":
		return 150.0
	case "EUR":
		return 0.92
	case "GBP":
		return 0.79
	}
	return 0
}

// ConvertUSD converts a USD amount to the preferred home currency. Falls
// back to the USD amount (no conversion) when the rate is unresolvable.
func (p *Portfolio) ConvertUSD(usdAmount float64, cnyRate float64) (float64, string) {
	if p == nil {
		return usdAmount, "USD"
	}
	home := strings.ToUpper(strings.TrimSpace(p.Preferences.HomeCurrency))
	if home == "" || home == "USD" {
		return usdAmount, "USD"
	}
	rate := CurrencyPairUSDRate(home, cnyRate)
	if rate <= 0 {
		return usdAmount, "USD"
	}
	return usdAmount * rate, home
}
// portfolio given current prices. Returns 0 if total is 0 or asset
// absent. Callers that want "compare to ceiling" do:
//
//	w := portfolio.WeightOf("btc", prices) * 100
//	ceil := portfolio.CeilingFor("btc")
//	overweight := ceil > 0 && w > ceil
func (p *Portfolio) WeightOf(asset string, prices map[string]float64) float64 {
	total := p.TotalValueUSD(prices)
	if total <= 0 {
		return 0
	}
	return p.PositionValueUSD(asset, prices[strings.ToLower(asset)]) / total
}
