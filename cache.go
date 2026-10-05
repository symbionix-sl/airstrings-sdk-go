package airstrings

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// diskCache stores verified bundles at {dir}/{project}/{env}/{locale}/bundle.json
// with the ETag beside it. A nil *diskCache is a no-op.
type diskCache struct{ dir string }

func newDiskCache(cfg Config) *diskCache {
	if cfg.CacheDir == "" {
		return nil
	}
	return &diskCache{dir: filepath.Join(cfg.CacheDir, cfg.ProjectID, cfg.EnvironmentID)}
}

// load returns nil data when nothing is cached.
func (d *diskCache) load(locale string) (data []byte, etag string, err error) {
	if d == nil {
		return nil, "", nil
	}
	data, err = os.ReadFile(filepath.Join(d.dir, locale, "bundle.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	tag, _ := os.ReadFile(filepath.Join(d.dir, locale, "etag"))
	return data, string(tag), nil
}

// save drops the old ETag first so a crash never pairs a new bundle with a stale ETag.
func (d *diskCache) save(locale string, data []byte, etag string) error {
	if d == nil {
		return nil
	}
	dir := filepath.Join(d.dir, locale)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	etagPath := filepath.Join(dir, "etag")
	if err := os.Remove(etagPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "bundle.json"), data); err != nil {
		return err
	}
	if etag == "" {
		return nil
	}
	return writeAtomic(etagPath, []byte(etag))
}

func (d *diskCache) remove(locale string) {
	if d != nil {
		_ = os.RemoveAll(filepath.Join(d.dir, locale))
	}
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}
