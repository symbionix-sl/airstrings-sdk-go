package fetch

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const path = "/org_1/proj_1/env_1/en/bundle.json"

var discard = slog.New(slog.DiscardHandler)

type host struct {
	*httptest.Server
	mu    sync.Mutex
	inm   []string
	paths []string
}

func (h *host) requests() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.paths)
}

func newHost(t *testing.T, handler http.HandlerFunc) *host {
	t.Helper()
	h := &host{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.inm = append(h.inm, r.Header.Get("If-None-Match"))
		h.paths = append(h.paths, r.URL.Path)
		h.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(h.Close)
	return h
}

func serve(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write([]byte(body))
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

func hang(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

func deadHost(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}

func fastFetcher(cdn, fallback string) *Fetcher {
	f := New(&http.Client{}, Hosts{CDN: cdn, Fallback: fallback}, discard)
	f.firstAttempt = 100 * time.Millisecond
	f.timeout = 2 * time.Second
	return f
}

func TestFetchReturnsBodyAndETag(t *testing.T) {
	cdn := newHost(t, serve("{}"))
	res, err := fastFetcher(cdn.URL, "").Fetch(context.Background(), path, "")
	if err != nil || string(res.Body) != "{}" || res.ETag != `"abc"` || res.NotModified {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if cdn.paths[0] != path || cdn.inm[0] != "" {
		t.Fatalf("path=%q inm=%q", cdn.paths[0], cdn.inm[0])
	}
}

func TestFetchNotModified(t *testing.T) {
	cdn := newHost(t, status(http.StatusNotModified))
	res, err := fastFetcher(cdn.URL, "").Fetch(context.Background(), path, `W/"abc"`)
	if err != nil || !res.NotModified {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if cdn.inm[0] != `W/"abc"` {
		t.Fatalf("If-None-Match = %q", cdn.inm[0])
	}
}

func TestFetchStatusError(t *testing.T) {
	cdn := newHost(t, status(http.StatusNotFound))
	_, err := fastFetcher(cdn.URL, "").Fetch(context.Background(), path, "")
	if !errors.Is(err, ErrHTTPStatus) || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchWithoutFallbackHasNoHeaderDeadline(t *testing.T) {
	cdn := newHost(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		serve("{}")(w, r)
	})
	if _, err := fastFetcher(cdn.URL, "").Fetch(context.Background(), path, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFetchWithoutFallbackDoesNotRetry(t *testing.T) {
	cdn := newHost(t, status(http.StatusServiceUnavailable))
	if _, err := fastFetcher(cdn.URL, "").Fetch(context.Background(), path, ""); err == nil {
		t.Fatal("want error")
	}
	if cdn.requests() != 1 {
		t.Fatalf("requests = %d", cdn.requests())
	}
}

func TestFailover(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"transport error": deadHost,
		"5xx":             func(t *testing.T) string { return newHost(t, status(http.StatusBadGateway)).URL },
		"header deadline": func(t *testing.T) string { return newHost(t, hang).URL },
		"body read error": func(t *testing.T) string {
			return newHost(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "1000")
				_, _ = w.Write([]byte("{"))
				panic(http.ErrAbortHandler)
			}).URL
		},
	}
	for name, cdn := range cases {
		t.Run(name, func(t *testing.T) {
			fallback := newHost(t, serve("fallback"))
			start := time.Now()
			res, err := fastFetcher(cdn(t), fallback.URL).Fetch(context.Background(), path, `"e1"`)
			if err != nil || string(res.Body) != "fallback" {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if fallback.requests() != 1 || fallback.inm[0] != `"e1"` || fallback.paths[0] != path {
				t.Fatalf("fallback saw %v %v", fallback.paths, fallback.inm)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("failover took %v", time.Since(start))
			}
		})
	}
}

func TestNoFailover(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"404": status(http.StatusNotFound),
		"403": status(http.StatusForbidden),
		"304": status(http.StatusNotModified),
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			cdn := newHost(t, handler)
			fallback := newHost(t, serve("fallback"))
			_, _ = fastFetcher(cdn.URL, fallback.URL).Fetch(context.Background(), path, `"e1"`)
			if fallback.requests() != 0 {
				t.Fatal("fallback was used")
			}
		})
	}
}

func TestHeaderDeadlineDoesNotLimitBody(t *testing.T) {
	cdn := newHost(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("cdn"))
	})
	fallback := newHost(t, serve("fallback"))
	res, err := fastFetcher(cdn.URL, fallback.URL).Fetch(context.Background(), path, "")
	if err != nil || string(res.Body) != "cdn" || fallback.requests() != 0 {
		t.Fatalf("res=%+v err=%v fallback=%d", res, err, fallback.requests())
	}
}

func TestRequestTimeoutCoversBody(t *testing.T) {
	cdn := newHost(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	f := fastFetcher(cdn.URL, "")
	f.timeout = 200 * time.Millisecond
	if _, err := f.Fetch(context.Background(), path, ""); err == nil {
		t.Fatal("want timeout")
	}
}

func TestStickyAfterFailover(t *testing.T) {
	cdn := newHost(t, status(http.StatusServiceUnavailable))
	fallback := newHost(t, serve("fallback"))
	f := fastFetcher(cdn.URL, fallback.URL)
	for range 3 {
		if _, err := f.Fetch(context.Background(), path, ""); err != nil {
			t.Fatal(err)
		}
	}
	if cdn.requests() != 1 || fallback.requests() != 3 {
		t.Fatalf("cdn=%d fallback=%d", cdn.requests(), fallback.requests())
	}
}

func TestStickyHostFailsBackToCDN(t *testing.T) {
	cdnUp := false
	var mu sync.Mutex
	cdn := newHost(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		up := cdnUp
		mu.Unlock()
		if !up {
			status(http.StatusServiceUnavailable)(w, r)
			return
		}
		serve("cdn")(w, r)
	})
	fallbackUp := true
	fallback := newHost(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		up := fallbackUp
		mu.Unlock()
		if !up {
			status(http.StatusServiceUnavailable)(w, r)
			return
		}
		serve("fallback")(w, r)
	})
	f := fastFetcher(cdn.URL, fallback.URL)
	if res, _ := f.Fetch(context.Background(), path, ""); string(res.Body) != "fallback" {
		t.Fatalf("first = %q", res.Body)
	}
	mu.Lock()
	cdnUp, fallbackUp = true, false
	mu.Unlock()
	if res, _ := f.Fetch(context.Background(), path, ""); string(res.Body) != "cdn" {
		t.Fatalf("second = %q", res.Body)
	}
	if res, _ := f.Fetch(context.Background(), path, ""); string(res.Body) != "cdn" || fallback.requests() != 2 {
		t.Fatalf("third = %q fallback=%d", res.Body, fallback.requests())
	}
}

func TestBothHostsFail(t *testing.T) {
	fallback := newHost(t, status(http.StatusServiceUnavailable))
	f := fastFetcher(deadHost(t), fallback.URL)
	if _, err := f.Fetch(context.Background(), path, ""); !errors.Is(err, ErrHTTPStatus) {
		t.Fatalf("err = %v", err)
	}
	if f.swapped.Load() {
		t.Fatal("swapped after failure")
	}
}

func TestCallerCancellationDoesNotFailOver(t *testing.T) {
	cdn := newHost(t, hang)
	fallback := newHost(t, serve("fallback"))
	f := fastFetcher(cdn.URL, fallback.URL)
	f.firstAttempt = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.Fetch(ctx, path, ""); err == nil {
		t.Fatal("want error")
	}
	if fallback.requests() != 0 {
		t.Fatal("fallback used after caller cancellation")
	}
}

func TestFailoverWarningNamesHostsOnly(t *testing.T) {
	var logs bytes.Buffer
	cdn := newHost(t, status(http.StatusInternalServerError))
	fallback := newHost(t, serve("fallback"))
	f := fastFetcher(cdn.URL, fallback.URL)
	f.log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := f.Fetch(context.Background(), path, ""); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, strings.TrimPrefix(cdn.URL, "http://")) || !strings.Contains(out, strings.TrimPrefix(fallback.URL, "http://")) {
		t.Fatalf("log = %s", out)
	}
	if strings.Contains(out, "proj_1") || strings.Contains(out, "bundle.json") {
		t.Fatalf("log leaks path: %s", out)
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	cdn := newHost(t, serve(strings.Repeat("x", maxBundleBytes+1)))
	if _, err := fastFetcher(cdn.URL, "").Fetch(context.Background(), path, ""); err == nil {
		t.Fatal("want error")
	}
}

func TestBootstrap(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    func(api string) Hosts
	}{
		{"with fallback", jsonBody(`{"cdn_base_url":"https://cdn.example/","fallback_base_url":"https://api.example/v1/cdn"}`),
			func(string) Hosts { return Hosts{CDN: "https://cdn.example", Fallback: "https://api.example/v1/cdn"} }},
		{"without fallback", jsonBody(`{"cdn_base_url":"https://cdn.example","future":1}`),
			func(string) Hosts { return Hosts{CDN: "https://cdn.example"} }},
		{"empty fallback", jsonBody(`{"cdn_base_url":"https://cdn.example","fallback_base_url":""}`),
			func(string) Hosts { return Hosts{CDN: "https://cdn.example"} }},
		{"invalid fallback", jsonBody(`{"cdn_base_url":"https://cdn.example","fallback_base_url":"not a url"}`),
			func(string) Hosts { return Hosts{CDN: "https://cdn.example"} }},
		{"missing cdn", jsonBody(`{"fallback_base_url":"https://api.example/v1/cdn"}`), defaults},
		{"invalid cdn", jsonBody(`{"cdn_base_url":"ftp://cdn.example"}`), defaults},
		{"bad json", jsonBody(`{`), defaults},
		{"wrong types", jsonBody(`{"cdn_base_url":5}`), defaults},
		{"500", status(http.StatusInternalServerError), defaults},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newHost(t, tc.handler)
			got, err := Bootstrap(context.Background(), &http.Client{}, api.URL+"/", discard)
			if got != tc.want(api.URL) || (err == nil) != (got != defaults("")) {
				t.Fatalf("got %+v, %v", got, err)
			}
			if api.paths[0] != "/v1/sdk/bootstrap" {
				t.Fatalf("path = %q", api.paths[0])
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		if got, err := Bootstrap(context.Background(), &http.Client{}, deadHost(t), discard); got != defaults("") || err == nil {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		api := newHost(t, hang)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if got, err := Bootstrap(ctx, &http.Client{}, api.URL, discard); got != defaults("") || err == nil {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}

func defaults(string) Hosts { return Hosts{CDN: DefaultCDN} }

func jsonBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}
