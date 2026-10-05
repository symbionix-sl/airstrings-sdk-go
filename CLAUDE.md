# AirStrings Go SDK

Go library for servers that fetches, verifies, caches, and serves Ed25519-signed localized string bundles. Module `github.com/symbionix-sl/airstrings-sdk-go`, package `airstrings`.

**Platform:** Go 1.26+ | **Dependencies:** standard library + `golang.org/x/text` (plural rules, number formatting) only

## Code Style

- `gofmt` formatting, idiomatic Go. Small focused internal packages.
- **No comments that restate code or explain changes.** Godoc on exported identifiers only (concise, pkg.go.dev-facing), plus a one-liner for a genuine gotcha.
- Deliberate shortcuts with a known ceiling carry a `ponytail:` comment naming the ceiling and the upgrade path.
- No interface with a single implementation. No config for values that never change.
- Errors: wrap with `%w` and context (`airstrings: <what>: %w`); sentinels for anything callers branch on.

## Non-Negotiables

Inherited from the parent project — these override everything else:

1. **Bundles are always signed.** No unsigned delivery path. Verification failure = hard error. Never expose unverified strings.
2. **Verification order matters.** key_id lookup -> signature decode -> canonical JSON -> Ed25519 verify -> format_version check -> project_id / locale match. Do not reorder.
3. **One trust pipeline.** Network, disk cache and seed all go through `bundle.Open`. Never parse and use a bundle any other way.
4. **Anti-downgrade.** Never replace a higher-revision bundle with a lower one for the same locale.
5. **Never panic, never block.** `New` only validates config and reads local files. Network runs in the background. No cache + no network = key names.
6. **No secrets in source.** Public keys come from the integrator. Never log keys, signatures or bundle bodies; failover logs name hosts only.
7. **Tests accompany every deliverable.** Each behavior lands with a test that was seen failing first.

## Architecture

```
doc.go                 # Package doc + usage
config.go              # Config, defaults, validation
airstrings.go          # Client: lifecycle, refresh loop, per-locale slots
localizer.go           # Localizer (T, Format), immutable snapshot per applied bundle
seed.go                # Startup: disk cache vs seed candidate selection
cache.go               # Disk cache (atomic temp+rename writes)
exposure.go            # Exposure type + dedupe + async delivery
errors.go              # Public sentinel errors (re-exported from internal packages)
internal/bundle/       # Parse, canonical JSON, Ed25519 verify, experiments verify, bucketing
internal/icu/          # ICU MessageFormat v1 parser + formatter
internal/fetch/        # Bootstrap + bundle fetcher with CDN failover
internal/bundletest/   # Test helper: builds signed bundles (imported by tests only)
```

### Layer Rules

| Package | May depend on | Never depends on |
|---------|---------------|------------------|
| `internal/bundle` | stdlib | icu, fetch, root |
| `internal/icu` | stdlib, `x/text` | bundle, fetch, root |
| `internal/fetch` | stdlib | bundle, icu, root |
| root `airstrings` | all internal packages | — |

### Data Flow

```
bootstrap -> Fetcher (CDN, fallback) -> bundle.Open (parse + verify + identity) -> disk cache -> snapshot -> atomic.Pointer per locale -> Localizer
```

### Concurrency Model

- Each locale has an `atomic.Pointer[snapshot]`. Reads load it once (a `Localizer` pins it) and never lock.
- A snapshot is built once per applied bundle: entries, compiled ICU messages, experiment selectors (bucket table). It is never mutated.
- Updates per locale are serialized by the slot mutex. User callbacks (`OnUpdate`, `OnError`) run after it is released; `OnExposure` runs on its own goroutine.
- Exposure dedupe uses `sync.Map` so repeat reads stay lock-free.

## Security Rules

- **Canonical JSON must be byte-identical across platforms.** `internal/bundle/canonical.go` is the source of truth; its tests assert the two contract fixtures from `docs/contracts/bundle-format.md` byte for byte. Any change requires a contract change.
- **Unknown key_id = reject.** Keys are matched verbatim against `Config.PublicKeys`.
- **Unknown format_version = reject**, even with a valid signature.
- **Signatures are exactly 64 bytes of unpadded base64url.**
- **Cache and seed are untrusted.** Re-verified on every load. Invalid cache entries are deleted; invalid seeds are reported and never cached.
- **Experiments soft-fail.** Missing/invalid `experiments_signature` or a malformed block strips all experiments; the bundle is still accepted.
- **Every candidate must match the configured project and the requested locale.**

## Build & Test

- **Test:** `go test -race ./...`
- **Live E2E (prod dogfood project):** `go test -tags live -run Live ./...`
- **Lint:** `gofmt -l .`, `go vet ./...`, `go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`
- **Vulns:** `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`
- **Benchmarks:** `go test -run x -bench . -benchmem ./...`

## Testing Standards

- **Crypto:** real Ed25519 keys via `internal/bundletest`; never mock verification. Contract fixtures and bucketing vectors are asserted directly.
- **Network:** `httptest` servers, or an `http.Client` whose transport fails (`offline`). Unit tests never reach the internet; only `live_test.go` (build tag `live`) does.
- **Timeouts:** fetch tests shorten the unexported `firstAttempt` / `timeout` fields.
- **Contract suites exist as named tests:** `TestIntegration*` (sdk-requirements.md) and `TestSeedConformance1..6` (bundled-fallback.md).

## v1 Scope Boundaries

**In v1:** fetch, verify, cache, serve strings for several locales at once; background refresh; ETag/304; CDN failover; seed from `fs.FS`; disk cache; variants + exposures; ICU MessageFormat v1 (args, number/integer/percent, plural, select, selectordinal).

**Not in v1 (do not build):** date/time/skeleton/spellout ICU arguments, rich-text tags, locale negotiation inside the SDK, delta bundles, metrics exporters.
