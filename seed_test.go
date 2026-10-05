package airstrings

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func cachePath(dir, locale string) string {
	return filepath.Join(dir, testProject, testEnv, locale, "bundle.json")
}

func writeCache(t *testing.T, dir, locale string, data []byte) {
	t.Helper()
	p := cachePath(dir, locale)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedConfig(s *server, seed fstest.MapFS) Config {
	cfg := s.config()
	cfg.HTTPClient = offline
	cfg.DisableSeed = false
	cfg.Seed = seed
	return cfg
}

func TestSeedConformance1OfflineColdStart(t *testing.T) {
	s := newServer(t)
	seed := fstest.MapFS{"en.json": {Data: s.key.Sign(textBundle("en", 3, "greeting", "Seeded"))}}
	c, errs := start(t, seedConfig(s, seed))
	if got := c.Localizer("en", "").T("greeting"); got != "Seeded" || c.Revision("en") != 3 {
		t.Fatalf("T = %q rev = %d", got, c.Revision("en"))
	}
	if len(errs.nonNetwork()) != 0 {
		t.Fatalf("errors: %v", errs.nonNetwork())
	}
}

func TestSeedConformance2TamperedSeedRejected(t *testing.T) {
	s := newServer(t)
	tampered := strings.Replace(string(s.key.Sign(textBundle("en", 3, "greeting", "Seeded"))), "Seeded", "Hacked", 1)
	cfg := seedConfig(s, fstest.MapFS{"en.json": {Data: []byte(tampered)}})
	cfg.CacheDir = t.TempDir()
	c, errs := start(t, cfg)
	requireErrorIs(t, errs.all(), ErrSignature)
	if got := c.Localizer("en", "").T("greeting"); got != "greeting" {
		t.Fatalf("T = %q", got)
	}
	if _, err := os.Stat(cachePath(cfg.CacheDir, "en")); !os.IsNotExist(err) {
		t.Fatal("tampered seed was cached")
	}
}

func TestSeedConformance3DowngradeIgnored(t *testing.T) {
	s := newServer(t)
	cached := s.key.Sign(textBundle("en", 5, "greeting", "Cached"))
	cfg := seedConfig(s, fstest.MapFS{"en.json": {Data: s.key.Sign(textBundle("en", 4, "greeting", "Seeded"))}})
	cfg.CacheDir = t.TempDir()
	writeCache(t, cfg.CacheDir, "en", cached)
	c, errs := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Cached" || c.Revision("en") != 5 {
		t.Fatalf("T = %q rev = %d", got, c.Revision("en"))
	}
	if after, _ := os.ReadFile(cachePath(cfg.CacheDir, "en")); string(after) != string(cached) {
		t.Fatal("cache overwritten by older seed")
	}
	if len(errs.nonNetwork()) != 0 {
		t.Fatalf("errors: %v", errs.nonNetwork())
	}
}

func TestSeedConformance4SeedNewerThanCache(t *testing.T) {
	s := newServer(t)
	seeded := s.key.Sign(textBundle("en", 6, "greeting", "Seeded"))
	cfg := seedConfig(s, fstest.MapFS{"en.json": {Data: seeded}})
	cfg.CacheDir = t.TempDir()
	writeCache(t, cfg.CacheDir, "en", s.key.Sign(textBundle("en", 5, "greeting", "Cached")))
	c, _ := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Seeded" || c.Revision("en") != 6 {
		t.Fatalf("T = %q rev = %d", got, c.Revision("en"))
	}
	if after, _ := os.ReadFile(cachePath(cfg.CacheDir, "en")); string(after) != string(seeded) {
		t.Fatal("winning seed not persisted to cache")
	}
}

func TestSeedConformance5MissingSeed(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 1, "greeting", "Hello"))
	for name, seed := range map[string]fstest.MapFS{"empty": {}, "other locale only": {"it.json": {Data: s.key.Sign(textBundle("it", 1, "greeting", "Ciao"))}}} {
		t.Run(name, func(t *testing.T) {
			cfg := s.config()
			cfg.DisableSeed = false
			cfg.Seed = seed
			c, errs := start(t, cfg)
			if c.Localizer("en", "").T("greeting") != "Hello" || len(errs.all()) != 0 {
				t.Fatalf("errors: %v", errs.all())
			}
		})
	}
	t.Run("default directory absent", func(t *testing.T) {
		t.Chdir(t.TempDir())
		cfg := s.config()
		cfg.DisableSeed = false
		c, errs := start(t, cfg)
		if c.Localizer("en", "").T("greeting") != "Hello" || len(errs.all()) != 0 {
			t.Fatalf("errors: %v", errs.all())
		}
	})
}

func TestSeedConformance6WrongProjectOrLocale(t *testing.T) {
	s := newServer(t)
	other := textBundle("en", 3, "greeting", "Other project")
	other.ProjectID = "proj_other0000000"
	t.Run("project", func(t *testing.T) {
		c, errs := start(t, seedConfig(s, fstest.MapFS{"en.json": {Data: s.key.Sign(other)}}))
		requireErrorIs(t, errs.all(), ErrProjectMismatch)
		if c.Localizer("en", "").T("greeting") != "greeting" {
			t.Fatal("wrong-project seed served")
		}
	})
	t.Run("locale", func(t *testing.T) {
		c, errs := start(t, seedConfig(s, fstest.MapFS{"en.json": {Data: s.key.Sign(textBundle("ja", 3, "greeting", "こんにちは"))}}))
		requireErrorIs(t, errs.all(), ErrLocaleMismatch)
		if c.Localizer("en", "").T("greeting") != "greeting" {
			t.Fatal("wrong-locale seed served")
		}
	})
}

func TestSeedTieKeepsCache(t *testing.T) {
	s := newServer(t)
	cfg := seedConfig(s, fstest.MapFS{"en.json": {Data: s.key.Sign(textBundle("en", 5, "greeting", "Seeded"))}})
	cfg.CacheDir = t.TempDir()
	writeCache(t, cfg.CacheDir, "en", s.key.Sign(textBundle("en", 5, "greeting", "Cached")))
	c, _ := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Cached" {
		t.Fatalf("T = %q", got)
	}
}

func TestSeedCorruptFileSurfaced(t *testing.T) {
	s := newServer(t)
	c, errs := start(t, seedConfig(s, fstest.MapFS{"en.json": {Data: []byte("{not json")}}))
	requireErrorIs(t, errs.all(), ErrBundleDecode)
	if c.Revision("en") != 0 {
		t.Fatal("corrupt seed applied")
	}
}

func TestSeedDefaultDirectory(t *testing.T) {
	s := newServer(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "airstrings", "bundles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "airstrings", "bundles", "en.json"), s.key.Sign(textBundle("en", 2, "greeting", "From disk")), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg := seedConfig(s, nil)
	cfg.Seed = nil
	c, _ := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "From disk" {
		t.Fatalf("T = %q", got)
	}

	cfg.DisableSeed = true
	c, _ = start(t, cfg)
	if c.Revision("en") != 0 {
		t.Fatal("seed loaded with DisableSeed")
	}
}

func TestNetworkNewerThanSeedWins(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 9, "greeting", "Network"))
	cfg := s.config()
	cfg.DisableSeed = false
	cfg.Seed = fstest.MapFS{"en.json": {Data: s.key.Sign(textBundle("en", 3, "greeting", "Seeded"))}}
	c, _ := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Network" {
		t.Fatalf("T = %q", got)
	}
}

func TestNetworkOlderThanSeedIgnored(t *testing.T) {
	s := newServer(t)
	s.publish(textBundle("en", 2, "greeting", "Network"))
	cfg := s.config()
	cfg.DisableSeed = false
	cfg.Seed = fstest.MapFS{"en.json": {Data: s.key.Sign(textBundle("en", 3, "greeting", "Seeded"))}}
	c, _ := start(t, cfg)
	if got := c.Localizer("en", "").T("greeting"); got != "Seeded" {
		t.Fatalf("T = %q", got)
	}
}

//go:embed testdata/seed/*.json
var embeddedSeed embed.FS

func TestSeedFromEmbedFS(t *testing.T) {
	seed, err := fs.Sub(embeddedSeed, "testdata/seed")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		OrganizationID:  "org_a1b2c3d4e5f6",
		ProjectID:       "proj_a1b2c3d4e5f6",
		EnvironmentID:   "env_a1b2c3d4e5f6",
		PublicKeys:      []string{"cubn9XZIRd+D8xwC3u9CLiAIJlvraVsK8foWPRuWtcQ="},
		Locales:         []string{"en", "it"},
		Seed:            seed,
		HTTPClient:      offline,
		RefreshInterval: -1,
	}
	c, errs := start(t, cfg)
	if got := c.Localizer("it", "").Format("items.count", map[string]any{"count": 3}); got != "3 articoli" {
		t.Fatalf("Format = %q", got)
	}
	if got := c.Localizer("en", "").T("greeting"); got != "Hello!" {
		t.Fatalf("T = %q", got)
	}
	if len(errs.nonNetwork()) != 0 {
		t.Fatalf("errors: %v", errs.nonNetwork())
	}
}
