# Guanfu Review Roadmap - 2026-05-10

This plan turns the latest project review into an implementation roadmap. The
goal is to improve two product modes without reintroducing China A-share or Hong
Kong stock logic:

- **Market reading**: make the current dashboard more reliable and more useful
  for CNY-based investors.
- **Forecasting**: improve backtest validity, calibration, and feature discipline.

## Principles

1. Fix data quality and evaluation before adding more model inputs.
2. Keep USD/CNY and central-bank data as context first; promote them into
   forecast features only after ablation proves value.
3. Keep HS300, A-share, and Hong Kong stock asset logic out of Guanfu.
4. Every new source must expose source health and a clear stale/missing state.
5. Every model change must be validated by asset-specific backtests.

## P0 - Data Source Health And Output Trust

Priority: highest.

### P0.1 Complete Source Health Coverage

Extend top-level output so each asset view clearly shows:

- source status: `ok`, `partial`, `missing`, `stale`, or `skip`
- latest available date
- fallback usage
- whether the source affects forecast or only market reading

Current state:

- BTC source health exists.
- USD/CNY and global central-bank rates have been added to source health.

Acceptance:

- `guanfu btc --forecast`, `guanfu qqq --forecast`, `guanfu spy --forecast`,
  and `guanfu gold --forecast` expose enough source-health context for users to
  judge forecast reliability without separately reading the refresh table.

### P0.2 Split Refresh Skip Semantics

Make refresh output distinguish:

- fresh skip: data is recent enough
- config skip: missing API key or optional configuration
- stale source: data exists but is too old for strong conclusions

Known cases:

- `stooq_putcall` currently skips when `STOOQ_APIKEY` is not set.
- `fred_pboc_interbank_rate` is stale and should be treated as macro background,
  not as a strong forecast input.

Acceptance:

- `guanfu refresh` makes actionability obvious: ignore, configure, replace, or
  investigate.

### P0.3 Update Data Source Documentation

Update `docs/DATA-SOURCES.md` with:

- required sources
- optional sources
- fallback sources
- stale thresholds
- impact on market reading vs forecast

Acceptance:

- A new operator can understand which sources are required for core behavior and
  which are optional context.

## P1 - Market Reading Enhancements

These changes can ship before model changes because they are dashboard/context
outputs, not forecast signals.

### P1.1 Add CNY Investor Lens

Add CNY-aware context for USD-denominated assets:

- `asset_price_cny`
- `asset_return_cny_30d`
- `asset_return_cny_90d`
- USD price return vs CNY return spread
- USD/CNY trend warning when CNY movement dominates local-currency returns

Acceptance:

- CNY-based users can see whether their realized local-currency experience is
  driven by asset return, FX return, or both.

### P1.2 Global Central Bank Panel

Keep the current US/EU/JP/CN macro context:

- Fed front-end rate
- ECB deposit rate
- BOJ call rate
- PBoC / China interbank proxy
- US-China rate spread
- developed-market policy-rate average

Acceptance:

- The dashboard explains dollar strength, rate spread, and liquidity backdrop
  without turning them into standalone buy/sell signals.

### P1.3 Explicit Non-Goals

Do not implement:

- HS300 asset logic
- A-share asset logic
- Hong Kong stock asset logic
- China macro forecasting as a Guanfu core asset pipeline

## P2 - Forecast Evaluation And Calibration

Priority: high. This is the core forecasting quality track.

### P2.1 Keep Backtests Asset-Specific

Current state:

- Non-BTC backtests were fixed to avoid BTC-only AHR and halving-cycle features.
- `qqq` and `spy` use equity feature bundles.
- `gold` uses gold feature bundles.
- `btc` keeps BTC core cycle features.

Next steps:

- Show feature bundle name in `backtest all`.
- Show feature coverage per asset and horizon.
- Show missing macro features that were dropped from the bundle.

Acceptance:

- Backtest reports make it clear which features were actually used.

### P2.2 Calibrate Before Adding Features

Latest asset-specific backtest result:

| Asset | Tests | Hit30d | Hit90d | Hit180d | PIT | CRPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| qqq | 28 | 64.3% | 85.7% | 78.6% | 0.56 | 0.1714 |
| spy | 28 | 67.9% | 78.6% | 82.1% | 0.56 | 0.1242 |
| btc | 62 | 64.5% | 69.3% | 69.3% | 0.45 | 0.5241 |
| gold | 69 | 52.2% | 62.3% | 62.3% | 0.50 | 0.1048 |

Interpretation:

- Directional signal is usable across the four current assets.
- QQQ/SPY are slightly optimistic by PIT.
- Gold calibration is currently best.

Next steps:

- Add asset/horizon-level conformal calibration.
- Compare calibration before and after for PIT and CRPS.
- Avoid adding new features if they improve hit rate but worsen calibration.

Acceptance:

- PIT should move toward `0.45-0.55` without materially worsening CRPS.

### P2.3 Backtest Guardrails

Required validation for any data-source or feature change:

- `go test ./...`
- `go vet ./...`
- `go run ./cmd/guanfu backtest all --plain`
- targeted single-asset backtest for the changed asset

Acceptance:

- No feature or source enters the forecast path without repeatable validation.

## P3 - New Data Sources

Add sources only after P0/P2 are stable.

### P3.1 BTC

Candidate sources:

- historical spot ETF flow
- MVRV Z-score
- NUPL
- SOPR
- stablecoin supply feature validation

Rule:

- On-chain features must be historical series, not latest-only dashboard values.

### P3.2 QQQ / SPY

Candidate sources:

- put/call replacement source or `STOOQ_APIKEY` configuration
- historical PE/PB if a reliable source exists
- CAPE ablation against current equity bundle

Rule:

- Do not use latest-only valuation snapshots as forecast features.

### P3.3 Gold

Candidate sources/features:

- real-yield change rate
- COT history
- DXY trend ablation

Rule:

- Prefer rate-of-change features over level-only macro features when the level is
  regime-dependent.

### P3.4 Global Macro

Current status:

- USD/CNY and central-bank rates are market-reading context only.

Promotion rule:

- They can become forecast features only after ablation shows improvement in
  hit rate, PIT, and CRPS.

## P4 - Product Output Separation

Separate user-facing output into two modes.

### Market Reading Mode

Purpose: answer "what is the current market environment?"

Should emphasize:

- price
- valuation
- macro context
- source health
- CNY lens
- stale warnings

### Forecast Mode

Purpose: answer "what did similar historical setups do next?"

Should emphasize:

- forecast horizon
- analog coverage
- confidence
- calibration quality
- invalidation conditions
- source health affecting forecast

## Execution Order

1. P0: source-health coverage, refresh status semantics, data-source docs.
2. P2: backtest report transparency and calibration.
3. P1: CNY investor lens and central-bank dashboard polish.
4. P3: new data sources, each gated by ablation.
5. P4: output separation once the underlying data and forecast paths are stable.

## Current Commit Baseline

The roadmap starts after these recent fixes:

- `643f9da chore: remove hs300 support`
- `241f0f1 feat: add global macro context`
- `beb3e0a fix: harden refresh data sources`
- `c446541 fix: surface macro source health`
- `2ba5ba5 fix: use asset-specific backtest features`
