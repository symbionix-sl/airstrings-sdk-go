package airstrings

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/text/language"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/fetch"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/icu"
)

// Client keeps verified string bundles for the configured locales up to date.
// It is safe for concurrent use. Create one per process and Close it on shutdown.
type Client struct {
	cfg       Config
	keys      bundle.Keys
	cache     *diskCache
	exposures *exposures
	slots     map[string]*slot
	order     []*slot

	fetchMu sync.Mutex
	fetcher *fetch.Fetcher

	ready     chan struct{}
	readyOnce sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// slot holds one locale. Reads load cur lock-free; mu serializes updates.
type slot struct {
	locale string
	icu    *icu.Locale
	cur    atomic.Pointer[snapshot]

	mu   sync.Mutex
	etag string
}

// New validates cfg, loads the disk cache and seed synchronously, and starts
// fetching in the background. It never blocks on the network; use WaitReady
// to wait for the first fetch.
func New(cfg Config) (*Client, error) {
	cfg, keys, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		cfg:       cfg,
		keys:      keys,
		cache:     newDiskCache(cfg),
		exposures: newExposures(cfg.OnExposure),
		slots:     make(map[string]*slot, len(cfg.Locales)),
		ready:     make(chan struct{}),
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	for _, l := range cfg.Locales {
		s := &slot{locale: l, icu: icu.NewLocale(language.Make(l))}
		c.slots[l] = s
		c.order = append(c.order, s)
	}
	for _, s := range c.order {
		c.loadLocal(s)
	}
	go c.run(ctx)
	return c, nil
}

func (c *Client) run(ctx context.Context) {
	defer close(c.done)
	defer c.readyOnce.Do(func() { close(c.ready) })
	_ = c.refresh(ctx)
	c.readyOnce.Do(func() { close(c.ready) })
	if c.cfg.RefreshInterval < 0 {
		return
	}
	t := time.NewTicker(c.cfg.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = c.refresh(ctx)
		}
	}
}

// WaitReady blocks until the first fetch of every locale has finished,
// successfully or not, or the Client is closed. It returns ctx.Err() if ctx
// ends first. Failures are reported to Config.OnError.
func (c *Client) WaitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Refresh fetches every locale now and returns the joined errors, which are
// also reported to Config.OnError. It is safe to call concurrently with reads
// and other refreshes.
func (c *Client) Refresh(ctx context.Context) error {
	return c.refresh(ctx)
}

// Close stops background refresh and waits for it to exit. It is idempotent.
// Reads keep working on the last applied strings.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		<-c.done
	})
	return nil
}

// Revision returns the revision of the strings applied for locale, or 0 if
// none are loaded or locale is not configured.
func (c *Client) Revision(locale string) int64 {
	if s, ok := c.slots[locale]; ok {
		if snap := s.cur.Load(); snap != nil {
			return snap.revision
		}
	}
	return 0
}

// Localizer returns a reader for locale pinned to the strings applied now.
// assignmentID selects experiment variants; "" serves base values. A locale
// that is not configured yields key names.
func (c *Client) Localizer(locale, assignmentID string) *Localizer {
	l := &Localizer{assignmentID: assignmentID, exposures: c.exposures}
	if s, ok := c.slots[locale]; ok {
		l.snap = s.cur.Load()
	}
	return l
}

func (c *Client) refresh(ctx context.Context) error {
	f := c.fetcherFor(ctx)
	errs := make([]error, len(c.order))
	var wg sync.WaitGroup
	for i, s := range c.order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.refreshLocale(ctx, f, s)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// fetcherFor bootstraps once; a failed bootstrap uses the default CDN for this
// round and is retried on the next.
func (c *Client) fetcherFor(ctx context.Context) *fetch.Fetcher {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	if c.fetcher != nil {
		return c.fetcher
	}
	hosts, err := fetch.Bootstrap(ctx, c.cfg.HTTPClient, c.cfg.APIBaseURL, c.cfg.Logger)
	f := fetch.New(c.cfg.HTTPClient, hosts, c.cfg.Logger)
	if err == nil {
		c.fetcher = f
	}
	return f
}

func (c *Client) refreshLocale(ctx context.Context, f *fetch.Fetcher, s *slot) error {
	s.mu.Lock()
	revision, err := c.fetchLocale(ctx, f, s)
	s.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			c.report(err)
		}
		return err
	}
	if revision > 0 && c.cfg.OnUpdate != nil {
		c.cfg.OnUpdate(s.locale, revision)
	}
	return nil
}

// fetchLocale returns the newly applied revision, or 0 if nothing changed.
func (c *Client) fetchLocale(ctx context.Context, f *fetch.Fetcher, s *slot) (int64, error) {
	path := "/" + c.cfg.OrganizationID + "/" + c.cfg.ProjectID + "/" + c.cfg.EnvironmentID + "/" + s.locale + "/bundle.json"
	res, err := f.Fetch(ctx, path, s.etag)
	if err != nil {
		return 0, fmt.Errorf("airstrings: fetch %s: %w", s.locale, err)
	}
	if res.NotModified {
		return 0, nil
	}
	b, err := bundle.Open(res.Body, c.keys, c.cfg.ProjectID, s.locale)
	if err != nil {
		return 0, fmt.Errorf("airstrings: %s bundle rejected: %w", s.locale, err)
	}
	cur := s.cur.Load()
	if cur != nil && b.Revision < cur.revision {
		c.cfg.Logger.Warn("airstrings: ignoring older bundle", "locale", s.locale, "revision", b.Revision, "current", cur.revision)
		return 0, nil
	}
	if err := c.cache.save(s.locale, res.Body, res.ETag); err != nil {
		c.cfg.Logger.Warn("airstrings: cache write failed", "locale", s.locale, "error", err)
	}
	s.etag = res.ETag
	if cur != nil && b.Revision == cur.revision {
		return 0, nil
	}
	s.cur.Store(newSnapshot(b, s.icu))
	return b.Revision, nil
}

func (c *Client) report(err error) {
	c.cfg.Logger.Error("airstrings: "+err.Error(), "error", err)
	if c.cfg.OnError != nil {
		c.cfg.OnError(err)
	}
}
