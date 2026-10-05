// Package fetch resolves delivery hosts and downloads bundles with CDN failover.
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultCDN       = "https://cdn.airstrings.com"
	maxBundleBytes   = 5 << 20
	maxBootstrapSize = 64 << 10
	requestTimeout   = 30 * time.Second
	headerDeadline   = 5 * time.Second
)

var ErrHTTPStatus = errors.New("airstrings: unexpected HTTP status")

type Hosts struct{ CDN, Fallback string }

// Bootstrap asks the API for the delivery hosts. On any failure it returns the
// default CDN with no fallback, together with the error.
func Bootstrap(ctx context.Context, hc *http.Client, apiBase string, log *slog.Logger) (Hosts, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	hosts, err := bootstrap(ctx, hc, strings.TrimRight(apiBase, "/")+"/v1/sdk/bootstrap")
	if err != nil {
		log.Warn("airstrings: bootstrap failed, using default CDN", "error", err)
		return Hosts{CDN: DefaultCDN}, err
	}
	return hosts, nil
}

func bootstrap(ctx context.Context, hc *http.Client, endpoint string) (Hosts, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Hosts{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Hosts{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Hosts{}, &statusError{code: resp.StatusCode}
	}
	var body struct {
		CDN      string `json:"cdn_base_url"`
		Fallback string `json:"fallback_base_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBootstrapSize)).Decode(&body); err != nil {
		return Hosts{}, err
	}
	cdn, ok := baseURL(body.CDN)
	if !ok {
		return Hosts{}, fmt.Errorf("invalid cdn_base_url %q", body.CDN)
	}
	fallback, _ := baseURL(body.Fallback)
	return Hosts{CDN: cdn, Fallback: fallback}, nil
}

func baseURL(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", false
	}
	return strings.TrimRight(s, "/"), true
}

// Fetcher downloads bundles from the CDN, failing over once to the fallback host.
// A host that succeeds as the second attempt stays first for the Fetcher's lifetime.
type Fetcher struct {
	hc           *http.Client
	log          *slog.Logger
	hosts        [2]string
	swapped      atomic.Bool
	firstAttempt time.Duration
	timeout      time.Duration
}

func New(hc *http.Client, hosts Hosts, log *slog.Logger) *Fetcher {
	return &Fetcher{
		hc:           hc,
		log:          log,
		hosts:        [2]string{hosts.CDN, hosts.Fallback},
		firstAttempt: headerDeadline,
		timeout:      requestTimeout,
	}
}

type Result struct {
	Body        []byte
	ETag        string
	NotModified bool
}

// Fetch GETs path from the preferred host, sending etag as If-None-Match.
func (f *Fetcher) Fetch(ctx context.Context, path, etag string) (Result, error) {
	swapped := f.swapped.Load()
	first, second := f.hosts[0], f.hosts[1]
	if swapped {
		first, second = second, first
	}
	if second == "" {
		return f.attempt(ctx, first+path, etag, 0)
	}
	res, err := f.attempt(ctx, first+path, etag, f.firstAttempt)
	if err == nil || !retryable(err) || ctx.Err() != nil {
		return res, err
	}
	f.log.Warn("airstrings: bundle fetch failed, retrying on other host", "host", hostOf(first), "retry_host", hostOf(second), "error", errorKind(err))
	res, err = f.attempt(ctx, second+path, etag, 0)
	if err != nil {
		return res, err
	}
	f.swapped.CompareAndSwap(swapped, !swapped)
	return res, nil
}

func (f *Fetcher) attempt(ctx context.Context, rawURL, etag string, deadline time.Duration) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	var timer *time.Timer
	if deadline > 0 {
		timer = time.AfterFunc(deadline, cancel)
	}
	resp, err := f.hc.Do(req)
	if timer != nil && !timer.Stop() && err == nil {
		resp.Body.Close()
		err = context.DeadlineExceeded
	}
	if err != nil {
		return Result{}, &transportError{err: err}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		return Result{NotModified: true}, nil
	default:
		return Result{}, &statusError{code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBundleBytes+1))
	if err != nil {
		return Result{}, &transportError{err: fmt.Errorf("read body: %w", err)}
	}
	if len(body) > maxBundleBytes {
		return Result{}, fmt.Errorf("airstrings: bundle exceeds %d bytes", maxBundleBytes)
	}
	return Result{Body: body, ETag: resp.Header.Get("ETag")}, nil
}

type transportError struct{ err error }

func (e *transportError) Error() string { return "airstrings: transport error: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("%v %d", ErrHTTPStatus, e.code) }
func (e *statusError) Unwrap() error { return ErrHTTPStatus }

func retryable(err error) bool {
	var te *transportError
	var se *statusError
	return errors.As(err, &te) || (errors.As(err, &se) && se.code >= 500)
}

func errorKind(err error) string {
	var se *statusError
	if errors.As(err, &se) {
		return se.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	return "transport error"
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	return u.Host
}
