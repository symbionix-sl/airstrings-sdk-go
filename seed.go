package airstrings

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
)

// loadLocal applies the best verified local candidate for s: the disk cache or
// the seed, highest revision wins and the cache wins ties. A winning seed is
// persisted to the cache. Invalid cache entries are deleted; invalid seeds are
// reported and never applied or cached.
func (c *Client) loadLocal(s *slot) {
	var best *bundle.Bundle
	data, etag, err := c.cache.load(s.locale)
	switch {
	case err != nil:
		c.report(fmt.Errorf("airstrings: read cached %s bundle: %w", s.locale, err))
	case data != nil:
		if best, err = bundle.Open(data, c.keys, c.cfg.ProjectID, s.locale); err != nil {
			c.cache.remove(s.locale)
			c.report(fmt.Errorf("airstrings: cached %s bundle discarded: %w", s.locale, err))
		}
	}
	if seeded, raw := c.loadSeed(s.locale); seeded != nil && (best == nil || seeded.Revision > best.Revision) {
		best, etag = seeded, ""
		if err := c.cache.save(s.locale, raw, ""); err != nil {
			c.cfg.Logger.Warn("airstrings: cache write failed", "locale", s.locale, "error", err)
		}
	}
	if best != nil {
		s.etag = etag
		s.cur.Store(newSnapshot(best, s.icu))
	}
}

func (c *Client) loadSeed(locale string) (*bundle.Bundle, []byte) {
	if c.cfg.Seed == nil {
		return nil, nil
	}
	data, err := fs.ReadFile(c.cfg.Seed, locale+".json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		c.report(fmt.Errorf("airstrings: read seed %s: %w", locale, err))
		return nil, nil
	}
	b, err := bundle.Open(data, c.keys, c.cfg.ProjectID, locale)
	if err != nil {
		c.report(fmt.Errorf("airstrings: seed %s rejected: %w", locale, err))
		return nil, nil
	}
	return b, data
}
