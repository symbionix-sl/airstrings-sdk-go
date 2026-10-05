//go:build live

package airstrings

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/fetch"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/icu"
)

var liveLocales = []string{"en", "it", "es", "de", "fr"}

func liveConfig() Config {
	return Config{
		OrganizationID:  "org_9eb19753e4cc",
		ProjectID:       "proj_88a4159b9ff6",
		EnvironmentID:   "env_988612ec1c49",
		PublicKeys:      []string{"Oom3qXrPcha8BPmYde+ciLMZwcUC7HlqDYfGlaJnSHk="},
		Locales:         liveLocales,
		RefreshInterval: -1,
		DisableSeed:     true,
	}
}

func startLive(t *testing.T, cfg Config) (*Client, *errorLog) {
	t.Helper()
	errs := &errorLog{}
	cfg.OnError = errs.add
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if e := errs.all(); len(e) != 0 {
		t.Fatalf("errors: %v", e)
	}
	return c, errs
}

func TestLiveProdDogfood(t *testing.T) {
	cfg := liveConfig()
	c, _ := startLive(t, cfg)
	keys, _ := bundle.ParseKeys(cfg.PublicKeys)

	hosts, err := fetch.Bootstrap(context.Background(), http.DefaultClient, defaultAPIBaseURL, slog.Default())
	if err != nil || hosts.Fallback == "" {
		t.Fatalf("bootstrap = %+v, %v", hosts, err)
	}

	for _, locale := range liveLocales {
		rev := c.Revision(locale)
		if rev <= 0 {
			t.Fatalf("%s: revision %d", locale, rev)
		}
		resp, err := http.Get(hosts.Fallback + "/" + cfg.OrganizationID + "/" + cfg.ProjectID + "/" + cfg.EnvironmentID + "/" + locale + "/bundle.json")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: fallback status %d, %v", locale, resp.StatusCode, err)
		}
		b, err := bundle.Open(data, keys, cfg.ProjectID, locale)
		if err != nil {
			t.Fatalf("%s: fallback bundle: %v", locale, err)
		}
		if b.Revision != rev {
			t.Fatalf("%s: fallback revision %d, client %d", locale, b.Revision, rev)
		}

		l := c.Localizer(locale, "")
		icuCount := 0
		for key, e := range b.Strings {
			if got := l.T(key); got != e.Value || got == key {
				t.Fatalf("%s: T(%q) = %q, want %q", locale, key, got, e.Value)
			}
			if e.Format == "icu" {
				icuCount++
				if _, err := icu.Compile(e.Value); err != nil {
					t.Errorf("%s: %s does not compile: %v (%q)", locale, key, err, e.Value)
				}
			}
		}
		count := l.Format("bundles.localeStringCount", map[string]any{"count": 1234})
		if !strings.Contains(count, "1") || strings.Contains(count, "{") {
			t.Fatalf("%s: Format = %q", locale, count)
		}
		t.Logf("%s: revision %d, %d strings (%d ICU) verified; %q", locale, rev, len(b.Strings), icuCount, count)
	}

	en := c.Localizer("en", "")
	if got := en.Format("bundles.localeStringCount", map[string]any{"count": 1}); got != "1 string" {
		t.Fatalf("en one = %q", got)
	}
	if got := en.Format("bundles.localeStringCount", map[string]any{"count": 1200}); got != "1,200 strings" {
		t.Fatalf("en other = %q", got)
	}
}

func TestLiveFailoverToFallbackHost(t *testing.T) {
	cfg := liveConfig()
	var cdnHits atomic.Int32
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "cdn.airstrings.com" {
			cdnHits.Add(1)
			return nil, errors.New("simulated CDN outage")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	c, _ := startLive(t, cfg)
	for _, locale := range liveLocales {
		if c.Revision(locale) <= 0 {
			t.Fatalf("%s not loaded via fallback", locale)
		}
	}
	if cdnHits.Load() == 0 {
		t.Fatal("CDN was never tried")
	}
	t.Logf("CDN attempts before sticky failover: %d", cdnHits.Load())
}
