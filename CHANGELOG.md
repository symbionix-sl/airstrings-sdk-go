# Changelog

All notable changes to `github.com/symbionix-sl/airstrings-sdk-go` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-10-05

### Added
- `Client` for Go servers: loads every configured locale, verifies Ed25519-signed bundles (main and experiments signatures), refreshes in the background and serves strings lock-free.
- `Localizer` per request: one bundle revision per Localizer, `T` for raw strings, `Format` for ICU MessageFormat (arguments, `number`/`integer`/`percent`, `plural` with `offset` and `=N`, `select`, `selectordinal`) with CLDR plural rules and locale number formatting. Formatting failures return the raw pattern; unknown keys return the key.
- CDN delivery with bootstrap-advertised fallback host: 5s header deadline on the first attempt, one retry on timeout, transport error or 5xx, sticky after a successful failover. ETag / 304 support.
- Bundled fallback seed from any `fs.FS` (works with `embed.FS`), defaulting to `./airstrings/bundles`; optional disk cache with atomic writes; highest verified revision wins across seed, cache and network.
- String variants: deterministic SHA-256 bucketing per assignment id, exposures delivered once per `(key, experiment, variant, assignment)` via `OnExposure`.
- Sentinel errors for `errors.Is`: `ErrSignature`, `ErrUnknownKeyID`, `ErrUnsupportedFormatVersion`, `ErrInvalidSignatureEncoding`, `ErrInvalidKeyIDEncoding`, `ErrBundleDecode`, `ErrProjectMismatch`, `ErrLocaleMismatch`, `ErrHTTPStatus`.
