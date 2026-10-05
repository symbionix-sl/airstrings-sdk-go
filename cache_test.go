package airstrings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskCacheRoundTrip(t *testing.T) {
	s := newServer(t)
	cfg := s.config()
	cfg.CacheDir = t.TempDir()
	published := s.publish(textBundle("en", 3, "greeting", "Hello"))
	first, _ := start(t, cfg)
	_ = first.Close()

	if got, _ := os.ReadFile(cachePath(cfg.CacheDir, "en")); string(got) != string(published) {
		t.Fatal("cached bytes differ from published artifact")
	}
	etag, err := os.ReadFile(filepath.Join(cfg.CacheDir, testProject, testEnv, "en", "etag"))
	if err != nil || len(etag) == 0 {
		t.Fatalf("etag not cached: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(cachePath(cfg.CacheDir, "en"))); len(entries) != 2 {
		t.Fatalf("leftover files: %v", entries)
	}

	c, errs := start(t, cfg)
	log := s.log()
	if last := log[len(log)-1]; last.ifNoneMatch != string(etag) {
		t.Fatalf("If-None-Match = %q, want %q", last.ifNoneMatch, etag)
	}
	if c.Localizer("en", "").T("greeting") != "Hello" || len(errs.all()) != 0 {
		t.Fatalf("errors: %v", errs.all())
	}
}

func TestCorruptCacheDeleted(t *testing.T) {
	s := newServer(t)
	cases := map[string][]byte{
		"tampered": []byte(strings.Replace(string(s.key.Sign(textBundle("en", 3, "greeting", "Hello"))), "Hello", "Evil", 1)),
		"garbage":  []byte("\x00\x01"),
		"other project": func() []byte {
			b := textBundle("en", 3, "greeting", "Hi")
			b.ProjectID = "proj_other0000000"
			return s.key.Sign(b)
		}(),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := s.config()
			cfg.HTTPClient = offline
			cfg.CacheDir = t.TempDir()
			writeCache(t, cfg.CacheDir, "en", data)
			c, errs := start(t, cfg)
			if c.Revision("en") != 0 {
				t.Fatal("invalid cache applied")
			}
			if _, err := os.Stat(cachePath(cfg.CacheDir, "en")); !os.IsNotExist(err) {
				t.Fatal("invalid cache entry not deleted")
			}
			if len(errs.nonNetwork()) == 0 {
				t.Fatal("invalid cache not reported")
			}
		})
	}
}
