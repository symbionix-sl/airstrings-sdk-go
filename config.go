package airstrings

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
)

const (
	defaultAPIBaseURL      = "https://api.airstrings.com"
	defaultRefreshInterval = 5 * time.Minute
	defaultSeedDir         = "airstrings/bundles"
)

// Config configures a Client. OrganizationID, ProjectID, EnvironmentID,
// PublicKeys and Locales are required.
type Config struct {
	OrganizationID string
	ProjectID      string
	EnvironmentID  string

	// PublicKeys are standard-base64 Ed25519 public keys exactly as shown in the
	// dashboard. List several during key rotation.
	PublicKeys []string

	// Locales are the BCP 47 locales to load, e.g. "en", "pt-BR".
	Locales []string

	// APIBaseURL defaults to https://api.airstrings.com.
	APIBaseURL string

	// RefreshInterval defaults to 5 minutes. A negative value disables
	// background refresh; the initial load still happens.
	RefreshInterval time.Duration

	// CacheDir enables a disk cache of verified bundles. Empty means memory only.
	CacheDir string

	// Seed holds bundled fallback files named {locale}.json, such as an
	// embed.FS of the directory written by `airstrings bundles pull`.
	// When nil, ./airstrings/bundles is used if it exists.
	Seed fs.FS

	// DisableSeed turns off seeding entirely.
	DisableSeed bool

	// HTTPClient defaults to a client with no timeout; the SDK applies its own deadlines.
	HTTPClient *http.Client

	// Logger defaults to discarding all output.
	Logger *slog.Logger

	// OnUpdate is called after a locale's strings change to a new revision.
	OnUpdate func(locale string, revision int64)

	// OnError receives every failure: rejected bundles, seed and cache errors,
	// and network errors. It may be called from New and from several goroutines.
	OnError func(error)

	// OnExposure is called on its own goroutine once per distinct
	// (key, experiment, variant, assignment) served by this Client.
	OnExposure func(Exposure)
}

func (cfg Config) normalize() (Config, bundle.Keys, error) {
	for _, id := range [...]struct{ name, value string }{
		{"OrganizationID", cfg.OrganizationID},
		{"ProjectID", cfg.ProjectID},
		{"EnvironmentID", cfg.EnvironmentID},
	} {
		if !safeSegment(id.value, 64, true) {
			return cfg, nil, fmt.Errorf("airstrings: invalid %s %q", id.name, id.value)
		}
	}
	keys, err := bundle.ParseKeys(cfg.PublicKeys)
	if err != nil {
		return cfg, nil, err
	}
	if len(cfg.Locales) == 0 {
		return cfg, nil, errors.New("airstrings: at least one locale is required")
	}
	seen := make(map[string]bool, len(cfg.Locales))
	for _, l := range cfg.Locales {
		if !safeSegment(l, 35, false) || seen[l] {
			return cfg, nil, fmt.Errorf("airstrings: invalid or duplicate locale %q", l)
		}
		seen[l] = true
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	if u, err := url.Parse(cfg.APIBaseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return cfg, nil, fmt.Errorf("airstrings: invalid APIBaseURL %q", cfg.APIBaseURL)
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = defaultRefreshInterval
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.DisableSeed {
		cfg.Seed = nil
	} else if cfg.Seed == nil {
		if info, err := os.Stat(defaultSeedDir); err == nil && info.IsDir() {
			cfg.Seed = os.DirFS(defaultSeedDir)
		}
	}
	return cfg, keys, nil
}

// safeSegment reports whether s is usable verbatim as a URL path and file name segment.
func safeSegment(s string, maxLen int, underscore bool) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || (underscore && c == '_')
		if !ok {
			return false
		}
	}
	return true
}
