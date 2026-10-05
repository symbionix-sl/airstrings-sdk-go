package airstrings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundletest"
)

func TestNewValidatesConfig(t *testing.T) {
	valid := Config{
		OrganizationID: testOrg,
		ProjectID:      testProject,
		EnvironmentID:  testEnv,
		PublicKeys:     []string{bundletest.NewKey().ID},
		Locales:        []string{"en", "pt-BR"},
		DisableSeed:    true,
		HTTPClient:     offline,
	}
	cases := map[string]func(c *Config){
		"missing organization": func(c *Config) { c.OrganizationID = "" },
		"missing project":      func(c *Config) { c.ProjectID = "" },
		"missing environment":  func(c *Config) { c.EnvironmentID = "" },
		"unsafe project":       func(c *Config) { c.ProjectID = "../etc" },
		"unsafe environment":   func(c *Config) { c.EnvironmentID = "env/x" },
		"no keys":              func(c *Config) { c.PublicKeys = nil },
		"bad key":              func(c *Config) { c.PublicKeys = []string{"nope"} },
		"no locales":           func(c *Config) { c.Locales = nil },
		"empty locale":         func(c *Config) { c.Locales = []string{""} },
		"unsafe locale":        func(c *Config) { c.Locales = []string{"../en"} },
		"locale with slash":    func(c *Config) { c.Locales = []string{"en/US"} },
		"duplicate locale":     func(c *Config) { c.Locales = []string{"en", "en"} },
		"bad api url":          func(c *Config) { c.APIBaseURL = "ftp://x" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if c, err := New(cfg); err == nil {
				_ = c.Close()
				t.Fatal("New succeeded")
			}
		})
	}
	cfg := valid
	cfg.PublicKeys = []string{"nope"}
	if _, err := New(cfg); !errors.Is(err, ErrInvalidKeyIDEncoding) {
		t.Fatalf("bad key err = %v", err)
	}
	c, err := New(valid)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestIntegrationHappyPath(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 3, "greeting", "Hello!"))
	c, errs := start(t, s.config())

	if got := c.Localizer("en", "").T("greeting"); got != "Hello!" {
		t.Fatalf("T = %q", got)
	}
	if c.Revision("en") != 3 {
		t.Fatalf("Revision = %d", c.Revision("en"))
	}
	if len(errs.all()) != 0 {
		t.Fatalf("errors: %v", errs.all())
	}
}

func TestIntegrationSignatureFailureKeepsCache(t *testing.T) {
	s := newServer(t)
	dir := t.TempDir()
	cfg := s.config()
	cfg.CacheDir = dir
	s.publish(textBundle("en", 1, "greeting", "Hello!"))
	first, _ := start(t, cfg)
	_ = first.Close()

	attacker := bundletest.NewKey()
	forged := attacker.Sign(textBundle("en", 2, "greeting", "Forged"))
	s.publishRaw("en", []byte(strings.Replace(string(forged), attacker.ID, s.key.ID, 1)))

	c, errs := start(t, cfg)
	requireErrorIs(t, errs.all(), ErrSignature)
	if got := c.Localizer("en", "").T("greeting"); got != "Hello!" || c.Revision("en") != 1 {
		t.Fatalf("T = %q rev = %d", got, c.Revision("en"))
	}
}

func TestIntegrationOfflineServesCache(t *testing.T) {
	s := newServer(t)
	cfg := s.config()
	cfg.CacheDir = t.TempDir()
	s.publish(textBundle("en", 4, "greeting", "Hello!"))
	first, _ := start(t, cfg)
	_ = first.Close()

	cfg.HTTPClient = offline
	c, errs := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Hello!" || c.Revision("en") != 4 {
		t.Fatalf("T = %q rev = %d", got, c.Revision("en"))
	}
	if len(errs.nonNetwork()) != 0 {
		t.Fatalf("errors: %v", errs.nonNetwork())
	}
}

func TestIntegrationColdOfflineReturnsKeyNames(t *testing.T) {
	s := newServer(t)
	cfg := s.config()
	cfg.HTTPClient = offline
	c, errs := start(t, cfg)
	l := c.Localizer("en", "user_1")
	if l.T("greeting") != "greeting" || l.Format("items.count", map[string]any{"n": 1}) != "items.count" {
		t.Fatal("key name fallback not served")
	}
	if c.Revision("en") != 0 {
		t.Fatal("revision without bundle")
	}
	requireErrorIs(t, errs.all(), errOffline)
}

func TestNetworkErrorsAreReportedAndReturned(t *testing.T) {
	s := newServer(t)
	s.setCDNStatus(http.StatusInternalServerError)
	c, errs := start(t, s.config())
	requireErrorIs(t, errs.all(), ErrHTTPStatus)
	if err := c.Refresh(context.Background()); !errors.Is(err, ErrHTTPStatus) {
		t.Fatalf("Refresh err = %v", err)
	}
}

func TestRefreshSendsETagAndHandlesNotModified(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello!"))
	var updates atomic.Int32
	cfg := s.config()
	cfg.OnUpdate = func(string, int64) { updates.Add(1) }
	c, _ := start(t, cfg)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := s.log()
	if len(log) != 2 || log[0].ifNoneMatch != "" || log[1].ifNoneMatch == "" {
		t.Fatalf("requests = %+v", log)
	}
	if updates.Load() != 1 || c.Localizer("en", "").T("greeting") != "Hello!" {
		t.Fatalf("updates = %d", updates.Load())
	}
}

func TestRefreshAppliesNewRevision(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello!"))
	type update struct {
		locale   string
		revision int64
	}
	updates := make(chan update, 4)
	cfg := s.config()
	cfg.OnUpdate = func(l string, r int64) { updates <- update{l, r} }
	c, _ := start(t, cfg)
	<-updates

	before := c.Localizer("en", "")
	s.publish(textBundle("en", 2, "greeting", "Hi!"))
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if u := <-updates; u != (update{"en", 2}) {
		t.Fatalf("update = %+v", u)
	}
	if got := c.Localizer("en", "").T("greeting"); got != "Hi!" {
		t.Fatalf("T = %q", got)
	}
	if got := before.T("greeting"); got != "Hello!" {
		t.Fatalf("Localizer snapshot changed: %q", got)
	}
}

func TestNetworkDowngradeIgnored(t *testing.T) {
	s := newServer(t)
	cfg := s.config()
	cfg.CacheDir = t.TempDir()
	s.publish(textBundle("en", 5, "greeting", "Five"))
	first, _ := start(t, cfg)
	_ = first.Close()
	cached, _ := os.ReadFile(filepath.Join(cfg.CacheDir, testProject, testEnv, "en", "bundle.json"))

	s.publish(textBundle("en", 4, "greeting", "Four"))
	c, errs := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Five" || c.Revision("en") != 5 {
		t.Fatalf("T = %q rev = %d", got, c.Revision("en"))
	}
	after, _ := os.ReadFile(filepath.Join(cfg.CacheDir, testProject, testEnv, "en", "bundle.json"))
	if string(after) != string(cached) {
		t.Fatal("cache overwritten by older revision")
	}
	if len(errs.all()) != 0 {
		t.Fatalf("errors: %v", errs.all())
	}
}

func TestNetworkBundleMustMatchProjectAndLocale(t *testing.T) {
	s := newServer(t)
	other := textBundle("en", 9, "greeting", "Other project")
	other.ProjectID = "proj_other0000000"
	s.publish(other)
	s.publish(textBundle("ja", 9, "greeting", "こんにちは"))
	s.publishRaw("it", s.key.Sign(textBundle("ja", 9, "greeting", "こんにちは")))
	cfg := s.config()
	cfg.Locales = []string{"en", "it"}
	c, errs := start(t, cfg)
	requireErrorIs(t, errs.all(), ErrProjectMismatch)
	requireErrorIs(t, errs.all(), ErrLocaleMismatch)
	if c.Revision("en") != 0 || c.Revision("it") != 0 {
		t.Fatal("mismatched bundle applied")
	}
}

func TestMultipleLocales(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello"))
	s.publish(textBundle("it", 7, "greeting", "Ciao"))
	cfg := s.config()
	cfg.Locales = []string{"en", "it"}
	c, _ := start(t, cfg)
	if c.Localizer("en", "").T("greeting") != "Hello" || c.Localizer("it", "").T("greeting") != "Ciao" {
		t.Fatal("wrong locale strings")
	}
	if c.Revision("it") != 7 || c.Revision("fr") != 0 || c.Localizer("fr", "").T("greeting") != "greeting" {
		t.Fatal("unexpected revision or unknown-locale fallback")
	}
}

func TestFailoverViaBootstrapFallback(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 2, "greeting", "Hello!"))
	s.setCDNStatus(http.StatusBadGateway)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		body := s.bundles["en"]
		s.mu.Unlock()
		_, _ = w.Write(body)
	}))
	defer fallback.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"cdn_base_url":"` + s.cdn.URL + `","fallback_base_url":"` + fallback.URL + `"}`))
	}))
	defer api.Close()
	cfg := s.config()
	cfg.APIBaseURL = api.URL
	c, errs := start(t, cfg)
	if c.Localizer("en", "").T("greeting") != "Hello!" || len(errs.all()) != 0 {
		t.Fatalf("errors: %v", errs.all())
	}
}

func TestBootstrapRetriedAfterFailure(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello!"))
	var apiUp atomic.Bool
	var transport http.RoundTripper = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !apiUp.Load() {
			return nil, errOffline
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	cfg := s.config()
	cfg.HTTPClient = &http.Client{Transport: transport}
	c, _ := start(t, cfg)
	if c.Revision("en") != 0 {
		t.Fatal("bundle loaded while offline")
	}
	apiUp.Store(true)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Revision("en") != 1 {
		t.Fatal("bootstrap not retried")
	}
}

func TestFormat(t *testing.T) {
	s := newServer(t)
	b := textBundle("en", 1, "plain", "Hello {name}")
	b.Strings["items.count"] = bundle.Entry{Value: "{count, plural, one {# item} other {# items}}", Format: "icu"}
	b.Strings["gender"] = bundle.Entry{Value: "{g, select, female {She} other {They}}", Format: "icu"}
	b.Strings["broken"] = bundle.Entry{Value: "{count, plural, one {# item}", Format: "icu"}
	b.Strings["dated"] = bundle.Entry{Value: "Due {d, date, short}", Format: "icu"}
	s.publish(b)
	c, _ := start(t, s.config())
	l := c.Localizer("en", "")
	cases := []struct {
		key  string
		args map[string]any
		want string
	}{
		{"items.count", map[string]any{"count": 1}, "1 item"},
		{"items.count", map[string]any{"count": 1200}, "1,200 items"},
		{"gender", map[string]any{"g": "female"}, "She"},
		{"plain", map[string]any{"name": "Ada"}, "Hello {name}"},
		{"items.count", nil, "{count, plural, one {# item} other {# items}}"},
		{"items.count", map[string]any{"count": "many"}, "{count, plural, one {# item} other {# items}}"},
		{"broken", map[string]any{"count": 1}, "{count, plural, one {# item}"},
		{"dated", map[string]any{"d": time.Now()}, "Due {d, date, short}"},
		{"missing.key", nil, "missing.key"},
	}
	for _, tc := range cases {
		if got := l.Format(tc.key, tc.args); got != tc.want {
			t.Errorf("Format(%q, %v) = %q, want %q", tc.key, tc.args, got, tc.want)
		}
	}
	if got := l.T("items.count"); got != "{count, plural, one {# item} other {# items}}" {
		t.Errorf("T returned %q, want raw pattern", got)
	}
}

func TestBackgroundRefreshAndClose(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello"))
	cfg := s.config()
	cfg.RefreshInterval = 20 * time.Millisecond
	c, _ := start(t, cfg)
	s.publish(textBundle("en", 2, "greeting", "Hi"))
	eventually(t, func() bool { return c.Revision("en") == 2 })

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	n := len(s.log())
	time.Sleep(100 * time.Millisecond)
	if len(s.log()) != n {
		t.Fatal("background refresh continued after Close")
	}
	if err := c.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReadyHonoursContextAndClose(t *testing.T) {
	block := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer api.Close()
	defer close(block)
	s := newServer(t)
	cfg := s.config()
	cfg.APIBaseURL = api.URL
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.WaitReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitReady = %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on in-flight bootstrap")
	}
	if err := c.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady after Close = %v", err)
	}
}

func TestCallbacksRunWithoutLocks(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello"))
	var client atomic.Pointer[Client]
	var once sync.Once
	result := make(chan error, 1)
	cfg := s.config()
	cfg.OnUpdate = func(string, int64) {
		once.Do(func() {
			for client.Load() == nil {
				time.Sleep(time.Millisecond)
			}
			result <- client.Load().Refresh(context.Background())
		})
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client.Store(c)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh from OnUpdate deadlocked")
	}
	_ = c.Close()
}

func TestConcurrentReadsAndRefresh(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "v1", "other", "v1"))
	c, _ := start(t, s.config())

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				l := c.Localizer("en", "user")
				if a, b := l.T("greeting"), l.Format("other", nil); a != b {
					t.Errorf("one Localizer saw two revisions: %q vs %q", a, b)
					return
				}
				_ = c.Revision("en")
			}
		}()
	}
	for rev := int64(2); rev <= 20; rev++ {
		v := "v" + string(rune('a'+rev))
		s.publish(textBundle("en", rev, "greeting", v, "other", v))
		if err := c.Refresh(context.Background()); err != nil {
			t.Error(err)
		}
	}
	cancel()
	wg.Wait()
	if c.Revision("en") != 20 {
		t.Fatalf("revision = %d", c.Revision("en"))
	}
}
