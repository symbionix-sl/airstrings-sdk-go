# airstrings-sdk-go

[![Go Reference](https://pkg.go.dev/badge/github.com/symbionix-sl/airstrings-sdk-go.svg)](https://pkg.go.dev/github.com/symbionix-sl/airstrings-sdk-go)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

Change your app's text without a redeploy.

The official **AirStrings Go SDK**: a server-side library that fetches, verifies, caches, and serves Ed25519-signed string bundles from the [AirStrings](https://airstrings.com) CDN.

```bash
go get github.com/symbionix-sl/airstrings-sdk-go
```

- **Signed bundles.** Every bundle is verified with Ed25519 before strings are exposed. Tampered or unsigned content is rejected, never served.
- **Built for servers.** All configured locales load together; each request picks its locale and assignment. Reads are lock-free; `T` does not allocate.
- **Always fresh.** Bundles refresh in the background (every 5 minutes by default) with ETag/304, and fail over to a second host when the CDN is unreachable.
- **Offline-safe.** Ship bundles inside your binary with `embed`, and optionally keep a disk cache. The highest verified revision always wins.
- **ICU MessageFormat.** Plurals, selects, ordinals and number formatting with CLDR rules for every locale.
- **Small.** Standard library plus `golang.org/x/text`.

---

## Install

```bash
go get github.com/symbionix-sl/airstrings-sdk-go
```

**Requirements:** Go 1.26+.

---

## Quick start

```go
import "github.com/symbionix-sl/airstrings-sdk-go"

client, err := airstrings.New(airstrings.Config{
	OrganizationID: "org_xxx",
	ProjectID:      "proj_xxx",
	EnvironmentID:  "env_xxx",
	PublicKeys:     []string{"BASE64_ED25519_PUBLIC_KEY"},
	Locales:        []string{"en", "it", "de"},
})
if err != nil {
	log.Fatal(err) // invalid configuration only; New never blocks on the network
}
defer client.Close()

// Optional: wait for the first fetch before serving traffic.
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
_ = client.WaitReady(ctx)

l := client.Localizer("en", "")
fmt.Println(l.T("greeting"))                                       // "Hello!"
fmt.Println(l.Format("items.count", map[string]any{"count": 1200})) // "1,200 items"
```

Create **one `Client` per process** and a **`Localizer` per request**. A `Localizer` is pinned to the bundle revision that was live when you created it, so everything rendered in one response is consistent even if a new revision arrives mid-request.

### net/http: locale per request

```go
var (
	supported = []language.Tag{language.English, language.Italian, language.German}
	matcher   = language.NewMatcher(supported) // golang.org/x/text/language
)

type ctxKey struct{}

func withStrings(client *airstrings.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, i := language.MatchStrings(matcher, r.URL.Query().Get("lang"), r.Header.Get("Accept-Language"))
		l := client.Localizer(supported[i].String(), currentUserID(r))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, l)))
	})
}

func handler(w http.ResponseWriter, r *http.Request) {
	l := r.Context().Value(ctxKey{}).(*airstrings.Localizer)
	fmt.Fprintln(w, l.T("greeting"))
}
```

Locale strings passed to `Localizer` must match one of `Config.Locales` exactly; any other locale returns key names.

---

## Bundled fallback (offline-safe builds)

Ship published, signed bundles inside your binary so a cold start with no network serves real strings instead of key names.

1. Pull the published bundles for your environment:

   ```bash
   airstrings bundles pull
   ```

2. Commit the resulting `airstrings/bundles/` seed directory and embed it:

   ```go
   //go:embed airstrings/bundles/*.json
   var bundles embed.FS

   seed, _ := fs.Sub(bundles, "airstrings/bundles")
   client, err := airstrings.New(airstrings.Config{
   	// ...
   	Seed: seed,
   })
   ```

Without `Seed`, the SDK reads `./airstrings/bundles/` relative to the working directory when it exists. Set `DisableSeed: true` to turn seeding off.

Seed files are untrusted input: each one runs the full Ed25519 verification, plus `project_id` and locale checks. The highest verified revision wins between disk cache and seed (ties go to the cache), a winning seed is persisted to the cache, and the network refresh still runs afterwards. A tampered or mismatched seed is reported to `OnError` and never served or cached; a missing seed directory or file is a silent no-op.

Keep the committed seed fresh by running `airstrings bundles pull` in CI or as a pre-release step.

### Disk cache

Set `CacheDir` to keep the last verified bundle of each locale on disk (`{CacheDir}/{project}/{environment}/{locale}/bundle.json`), so a restart without network still serves the latest strings. Writes are atomic and every cached bundle is re-verified on load; an invalid entry is deleted.

---

## String variants (experiments)

Strings can carry A/B variants defined in the dashboard. Pass a stable assignment id (for example the user id) to `Localizer` and the SDK selects each experiment's variant deterministically — no server round-trip, no state to persist. The same id always resolves to the same variant, in every locale.

```go
client, err := airstrings.New(airstrings.Config{
	// ...
	OnExposure: func(e airstrings.Exposure) {
		analytics.Track("experiment_exposure", e.Key, e.ExperimentID, e.Variant, e.Locale, e.AssignmentID)
	},
})

l := client.Localizer("en", user.ID)
l.T("checkout.cta") // variant or control, chosen by user.ID
```

> **Exposure fires once per Client.** `OnExposure` is called on its own goroutine, never inside the read, once per distinct `(key, experiment, variant, assignment)` for the lifetime of the `Client`. Locale is not part of the key. This attributes a user to a variant (join to conversions on `AssignmentID`); for per-render impressions, emit your own event where you render. On very long-running servers the dedupe set is reset after 100,000 entries, so an exposure may be delivered again after that.

An empty assignment id serves base values. Experiment definitions are covered by their own signature: if it is missing or invalid, or an allocation is malformed, the SDK serves base values and records no exposure — the bundle itself is still used.

---

## API

### `airstrings.New(cfg Config) (*Client, error)`

| Field             | Type                          | Required | Description |
|-------------------|-------------------------------|----------|-------------|
| `OrganizationID`  | `string`                      | yes      | Your AirStrings organization id. |
| `ProjectID`       | `string`                      | yes      | Project id. |
| `EnvironmentID`   | `string`                      | yes      | Environment id (e.g. production, staging). |
| `PublicKeys`      | `[]string`                    | yes      | One or more standard-base64 Ed25519 public keys, exactly as shown in the dashboard. Several keys support rotation. |
| `Locales`         | `[]string`                    | yes      | BCP 47 locales to load, e.g. `"en"`, `"pt-BR"`. |
| `APIBaseURL`      | `string`                      | no       | Defaults to `https://api.airstrings.com`. |
| `RefreshInterval` | `time.Duration`               | no       | Background refresh period. Default 5 minutes; negative disables background refresh (the first fetch still happens). |
| `CacheDir`        | `string`                      | no       | Enables the disk cache. Empty keeps bundles in memory only. |
| `Seed`            | `fs.FS`                       | no       | Bundled fallback files named `{locale}.json`. Default: `./airstrings/bundles` if present. |
| `DisableSeed`     | `bool`                        | no       | Turns seeding off. |
| `HTTPClient`      | `*http.Client`                | no       | Custom transport (proxies, tracing). The SDK applies its own timeouts. |
| `Logger`          | `*slog.Logger`                | no       | Structured logs. Default: discarded. |
| `OnUpdate`        | `func(locale string, revision int64)` | no | Called after a locale moves to a new revision. |
| `OnError`         | `func(error)`                 | no       | Called for every failure: rejected bundles, seed and cache errors, network errors. |
| `OnExposure`      | `func(Exposure)`              | no       | Experiment exposures, see above. |

### `*Client`

- **`Localizer(locale, assignmentID string) *Localizer`** — Reader pinned to the current revision of `locale`.
- **`WaitReady(ctx) error`** — Blocks until the first fetch of every locale finished (successfully or not) or the client is closed.
- **`Refresh(ctx) error`** — Fetches all locales now. Safe to call concurrently.
- **`Revision(locale string) int64`** — Applied revision, `0` when nothing is loaded.
- **`Close() error`** — Stops background refresh. Idempotent. Reads keep working.

### `*Localizer`

- **`T(key string) string`** — The raw string (ICU patterns unformatted), the selected variant when an assignment is set, or `key` when unknown.
- **`Format(key string, args map[string]any) string`** — Formats an ICU string. Text strings are returned as-is. Never panics: a missing or mistyped argument, or an unsupported pattern (`date`, `time`, skeletons, `spellout`), returns the raw pattern; an unknown key returns `key`. Numeric arguments must be Go numeric types (`int`, `float64`, …).

---

## Error handling

`New` returns an error only for invalid configuration. Everything else is reported to `OnError` (and the `Logger`), and `Refresh` returns the same errors joined. Test with `errors.Is`:

```go
OnError: func(err error) {
	switch {
	case errors.Is(err, airstrings.ErrSignature), errors.Is(err, airstrings.ErrUnknownKeyID):
		alert("AirStrings bundle rejected", err) // tampering or a key not in PublicKeys
	case errors.Is(err, airstrings.ErrHTTPStatus):
		log.Printf("CDN error: %v", err)
	default:
		log.Printf("airstrings: %v", err)
	}
},
```

| Error | Meaning |
|-------|---------|
| `ErrSignature` | Signature does not match the content. |
| `ErrUnknownKeyID` | Bundle signed by a key not in `PublicKeys`. |
| `ErrUnsupportedFormatVersion` | Bundle format newer than this SDK. |
| `ErrInvalidSignatureEncoding` | Signature is not 64 bytes of unpadded base64url. |
| `ErrInvalidKeyIDEncoding` | A configured public key is malformed (returned by `New`). |
| `ErrBundleDecode` | Not a valid bundle. |
| `ErrProjectMismatch`, `ErrLocaleMismatch` | A validly signed bundle for another project or locale. |
| `ErrHTTPStatus` | Unexpected HTTP status from the CDN. |

A rejected bundle never replaces the strings already being served.

---

## Security model

- Bundles are signed with Ed25519 over a deterministic canonical JSON representation that covers `format_version`, `project_id`, `locale`, `revision`, `created_at`, and `strings`. This prevents tampering, locale swapping, and bundle substitution.
- Every candidate — network, disk cache, seed — is verified against your configured keys and must match the configured project and requested locale.
- A lower revision never replaces a higher one (anti-downgrade).
- Experiments carry a separate signature; any problem degrades to base values, never to unverified content.
- Public keys are provided by you at init. The SDK never logs them; failover warnings name hosts only.

See the bundle format contract in the AirStrings docs for the full specification.

---

## License

MIT © [Symbionix](https://symbionix.io)

## Links

- **Website:** https://airstrings.com
- **Repository:** https://github.com/symbionix-sl/airstrings-sdk-go
- **Issues:** https://github.com/symbionix-sl/airstrings-sdk-go/issues
- **Changelog:** [CHANGELOG.md](./CHANGELOG.md)
