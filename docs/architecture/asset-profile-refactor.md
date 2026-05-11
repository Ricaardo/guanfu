# ADR: Asset Profile Architecture

Status: proposed for staged migration

Date: 2026-05-11

## Context

Guanfu started as a BTC-first reading and forecasting system. The current code
already supports BTC, QQQ, SPY, Gold, and arbitrary US stock forecasts, but the
asset-specific knowledge is spread across several places:

- `pkg/engine/asset_*.go` chooses snapshot, panel, verdict, and forecast wiring.
- `pkg/forecast/features/bundles.go` chooses feature bundles.
- `pkg/forecast/forecast.go` owns default horizons and horizon weight rules.
- `pkg/forecast/reliability.go` owns static reliability claims.
- `cmd/guanfu/cli_backtest.go` repeats asset-to-feature mappings.
- `skill/SKILL.md` describes all assets, but remains BTC-first in structure.

This creates a hidden coupling: non-BTC assets no longer use BTC-only AHR or
halving features, but they still inherit BTC-era assumptions through shared
forecast scaling, shared output schema, shared CLI/MCP contracts, and shared
skill wording.

The problem is broader than forecasting. Market reading also needs asset-aware
domain definitions, evidence rules, source health expectations, and downgrade
behavior.

## Decision

Introduce `AssetProfile` as the single source of asset knowledge. The forecast
engine, panel builders, CLI, MCP, and skill should read asset-specific policy
from profiles instead of maintaining parallel switch statements.

The target layering is:

```text
DataSource        Raw source adapters and PriceStore refresh policies
AssetProfile      Asset identity, class, capabilities, and contracts
ReadingLens       Panel domains, indicators, thresholds, and verdict rules
ForecastProfile   Features, normalization, horizons, reliability, calibration
SkillProfile      Human/AI reading protocol and caveat language
```

The generic forecast engine remains shared. Asset-specific assumptions move out
of `forecast.Build` and into `ForecastProfile`.

## Target Interfaces

Illustrative Go shape:

```go
type AssetClass string

const (
    AssetClassBTC         AssetClass = "btc"
    AssetClassEquityIndex AssetClass = "equity_index"
    AssetClassGold        AssetClass = "gold"
    AssetClassUSStock     AssetClass = "us_stock"
)

type AssetProfile interface {
    Key() string
    Class() AssetClass
    DisplayName() string
    DataSources() []SourceSpec
    ReadingLens() ReadingLens
    ForecastProfile() ForecastProfile
    SkillProfile() SkillProfile
}

type ReadingLens struct {
    Domains       []DomainSpec
    VerdictPolicy VerdictPolicy
    SourcePolicy  SourcePolicy
}

type ForecastProfile struct {
    Horizons       []int
    TopK           map[int]int
    Extractors     []FeatureSpec
    Normalizers    map[string]FeatureNormalizer
    HorizonWeights map[int]map[string]float64
    Reliability    map[int]ReliabilityCell
    Calibration    map[int]ConformalCalibration
}
```

Profiles may share helpers, but each profile owns the semantic meaning of its
domains and features.

## Reading Lenses

BTC remains an 8-domain crypto-cycle lens:

```text
cycle / valuation / network / positioning / macro / flow / technical / cross_asset
```

Equity indices use a different lens:

```text
valuation / earnings_macro / liquidity_rates / credit_risk /
breadth_sentiment / options_positioning / technical
```

Gold uses a macro/positioning lens:

```text
real_yield / usd / inflation / positioning / risk_off / technical / cross_asset
```

Arbitrary US stocks use a conservative single-name lens:

```text
price_action / valuation / fundamentals / macro / sector_relative /
flow_sentiment / event_risk / technical
```

Shared technical indicators are implementation helpers, not a shared reading
philosophy. For example, RSI can appear in all profiles, but the threshold,
weight, and caveat may differ by asset class.

## Forecast Rules

Feature extraction should be split into two phases:

1. Raw feature calculation: return value, date, source, and feature name.
2. Profile normalization: scale, clipping, direction, and weight.

This prevents BTC volatility assumptions from leaking into QQQ, SPY, Gold, or
single stocks. For example, a 30-day return scale suitable for BTC is not a safe
default for SPY or a low-volatility large-cap stock.

The generic engine should not know that Gold has weak 30d history or that QQQ
uses put/call data. It should receive a fully resolved `ForecastProfile`.

## Skill Contract

`skill/SKILL.md` becomes a router and protocol overview. Asset-specific reading
instructions live in `skill/profiles/`:

- `btc.md`
- `equity_index.md`
- `gold.md`
- `us_stock.md`

Reusable contracts live in `skill/contracts/`:

- `asset-profile.md`
- `adding_asset.md`

Skill consumers must load:

1. `tier1.md` for data/reliability rules.
2. The relevant asset profile.
3. `tier2.md` only when making a decision-style synthesis.

## Migration Plan

### Phase 1 - Documentation And Contracts

- Add this ADR.
- Add skill profile files and onboarding contract.
- Update skill README and top-level SKILL routing table.
- Record the architectural boundary in `docs/internals.md`.

### Phase 2 - Profile Registry

- Add `pkg/profile` or `pkg/assetprofile`.
- Move default horizons, reliability cells, conformal calibration, and horizon
  weights behind profile methods.
- Keep existing `engine.Asset` implementations as orchestrators.

Acceptance:

- CLI, MCP, backtest, and asset `BuildForecast` all read horizon and reliability
  data from the same profile registry.

### Phase 3 - Reading Lens Refactor

- Replace BTC-shaped `BuildVerdict` reuse with profile-specific `ReadingLens`
  verdict policies.
- Split Gold from `BuildEquityPanel`; keep shared technical helper functions.
- Make `IndicatorPanel` capable of carrying profile domain metadata, not only
  fixed BTC-era domain names.

Acceptance:

- Gold no longer depends on an equity panel builder for semantic domains.
- QQQ/SPY and arbitrary US stocks do not expose BTC-only domain expectations.

### Phase 4 - Forecast Feature Normalization

- Change extractors to emit raw values.
- Move normalization and weights to `ForecastProfile`.
- Add per-profile feature-scale tests.

Acceptance:

- BTC, equity index, gold, and US stock profiles can use the same raw feature
  names with different normalization scales.
- Backtest reports show profile name and profile version.

### Phase 5 - Arbitrary Asset Onboarding

- Use `skill/contracts/adding_asset.md` as the required checklist.
- Do not add a new asset class until it has a reading lens, forecast profile,
  source-health policy, and reliability downgrade policy.

## Non-Goals

- Do not rewrite the kNN/conformal engine from scratch.
- Do not reintroduce A-share, Hong Kong, or HS300 asset logic into the core.
- Do not let latest-only data enter forecast features without historical replay
  and ablation.
- Do not promote CMC/CoinGecko context into forecast features until a stable
  historical series and backtest evidence exist.

## Risks

- Larger profile surface area can become boilerplate. Mitigation: share helper
  constructors for common technical indicators and source policies.
- Backward-compatible MCP output may constrain schema cleanup. Mitigation:
  expose profile metadata additively and keep old aliases during migration.
- Static reliability tables can become stale. Mitigation: profile version and
  backtest as-of dates must be rendered in reports.

## Open Questions

- Should arbitrary US stocks share one class-level reliability table initially,
  or should unsupported tickers always be labeled untested?
- Should sector ETF support be a separate `sector_etf` class or a specialization
  of `equity_index`?
- Should profile files be code-only, data-driven YAML/JSON, or a hybrid?

## Success Criteria

- There is one source of truth for each asset's horizons, feature bundle,
  reliability, calibration, source-health policy, and skill reading protocol.
- Adding a new asset does not require editing unrelated BTC, QQQ, SPY, or Gold
  logic.
- A user reading `guanfu stock AAPL --forecast` sees single-name caveats that
  are materially different from BTC or index caveats.
- Backtest output names the active profile and profile version.
