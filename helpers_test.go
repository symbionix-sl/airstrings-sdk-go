package airstrings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundletest"
)

const (
	testOrg     = "org_test12345678"
	testProject = "proj_test12345678"
	testEnv     = "env_test12345678"
)

type request struct{ host, locale, ifNoneMatch string }

// server is a fake API + CDN pair that serves published bundles with R2-style ETags.
type server struct {
	key      bundletest.Key
	api, cdn *httptest.Server

	mu       sync.Mutex
	bundles  map[string][]byte
	cdnFail  int
	requests []request
}

func newServer(t testing.TB) *server {
	t.Helper()
	s := &server{key: bundletest.NewKey(), bundles: map[string][]byte{}}
	s.cdn = httptest.NewServer(http.HandlerFunc(s.serveBundle))
	s.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sdk/bootstrap" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"cdn_base_url":"` + s.cdn.URL + `"}`))
	}))
	t.Cleanup(s.api.Close)
	t.Cleanup(s.cdn.Close)
	return s
}

func (s *server) serveBundle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parts) != 5 || parts[0] != testOrg || parts[1] != testProject || parts[2] != testEnv || parts[4] != "bundle.json" {
		http.NotFound(w, r)
		return
	}
	s.requests = append(s.requests, request{host: r.Host, locale: parts[3], ifNoneMatch: r.Header.Get("If-None-Match")})
	if s.cdnFail != 0 {
		w.WriteHeader(s.cdnFail)
		return
	}
	body, ok := s.bundles[parts[3]]
	if !ok {
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	_, _ = w.Write(body)
}

func (s *server) publish(b *bundle.Bundle) []byte {
	data := s.key.Sign(b)
	s.publishRaw(b.Locale, data)
	return data
}

func (s *server) publishRaw(locale string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bundles[locale] = data
}

func (s *server) setCDNStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cdnFail = code
}

func (s *server) log() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]request(nil), s.requests...)
}

func (s *server) config() Config {
	return Config{
		OrganizationID:  testOrg,
		ProjectID:       testProject,
		EnvironmentID:   testEnv,
		PublicKeys:      []string{s.key.ID},
		Locales:         []string{"en"},
		APIBaseURL:      s.api.URL,
		RefreshInterval: -1,
		DisableSeed:     true,
	}
}

func textBundle(locale string, revision int64, kv ...string) *bundle.Bundle {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return bundletest.New(testProject, locale, revision, m)
}

var errOffline = errors.New("offline")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// offline is an HTTP client for which every request fails without touching the network.
var offline = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
	return nil, errOffline
})}

// errorLog collects errors delivered to OnError.
type errorLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *errorLog) add(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

func (l *errorLog) all() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]error(nil), l.errs...)
}

// nonNetwork drops errors caused by the offline transport.
func (l *errorLog) nonNetwork() []error {
	var out []error
	for _, err := range l.all() {
		if !errors.Is(err, errOffline) {
			out = append(out, err)
		}
	}
	return out
}

func start(t *testing.T, cfg Config) (*Client, *errorLog) {
	t.Helper()
	errs := &errorLog{}
	if cfg.OnError == nil {
		cfg.OnError = errs.add
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	return c, errs
}

func requireErrorIs(t *testing.T, errs []error, target error) {
	t.Helper()
	for _, err := range errs {
		if errors.Is(err, target) {
			return
		}
	}
	t.Fatalf("errors %v do not include %v", errs, target)
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
