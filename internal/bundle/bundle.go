// Package bundle parses and verifies AirStrings signed bundles and selects experiment variants.
package bundle

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrUnknownKeyID             = errors.New("airstrings: unknown key_id")
	ErrSignature                = errors.New("airstrings: signature verification failed")
	ErrUnsupportedFormatVersion = errors.New("airstrings: unsupported format_version")
	ErrInvalidSignatureEncoding = errors.New("airstrings: invalid signature encoding")
	ErrInvalidKeyIDEncoding     = errors.New("airstrings: invalid key_id encoding")
	ErrBundleDecode             = errors.New("airstrings: bundle decode failed")
	ErrProjectMismatch          = errors.New("airstrings: project_id mismatch")
	ErrLocaleMismatch           = errors.New("airstrings: locale mismatch")
)

type Bundle struct {
	FormatVersion        int64            `json:"format_version"`
	ProjectID            string           `json:"project_id"`
	Locale               string           `json:"locale"`
	Revision             int64            `json:"revision"`
	CreatedAt            string           `json:"created_at"`
	KeyID                string           `json:"key_id"`
	Signature            string           `json:"signature"`
	ExperimentsSignature string           `json:"experiments_signature,omitempty"`
	Strings              map[string]Entry `json:"strings"`
}

type Entry struct {
	Value      string      `json:"value"`
	Format     string      `json:"format"`
	Experiment *Experiment `json:"experiment,omitempty"`
}

type Experiment struct {
	ID         string            `json:"id"`
	Allocation map[string]int64  `json:"allocation"`
	Variants   map[string]string `json:"variants"`
}

// Keys maps a configured key_id (standard base64, verbatim) to its public key.
type Keys map[string]ed25519.PublicKey

func ParseKeys(encoded []string) (Keys, error) {
	if len(encoded) == 0 {
		return nil, fmt.Errorf("%w: no public keys", ErrInvalidKeyIDEncoding)
	}
	keys := make(Keys, len(encoded))
	for i, s := range encoded {
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: public key %d must be standard base64 of %d bytes", ErrInvalidKeyIDEncoding, i, ed25519.PublicKeySize)
		}
		keys[s] = ed25519.PublicKey(raw)
	}
	return keys, nil
}

// Open runs the full trust pipeline: parse, verify, then check the bundle belongs
// to projectID and locale. Experiments that fail verification are stripped.
func Open(data []byte, keys Keys, projectID, locale string) (*Bundle, error) {
	b, experimentsWellFormed, err := parse(data)
	if err != nil {
		return nil, err
	}
	pub, err := verify(b, keys)
	if err != nil {
		return nil, err
	}
	if b.ProjectID != projectID {
		return nil, fmt.Errorf("%w: bundle has %q, want %q", ErrProjectMismatch, b.ProjectID, projectID)
	}
	if b.Locale != locale {
		return nil, fmt.Errorf("%w: bundle has %q, want %q", ErrLocaleMismatch, b.Locale, locale)
	}
	if !experimentsWellFormed || !verifyExperiments(b, pub) {
		stripExperiments(b)
	}
	return b, nil
}

type wireBundle struct {
	FormatVersion        int64                 `json:"format_version"`
	ProjectID            string                `json:"project_id"`
	Locale               string                `json:"locale"`
	Revision             int64                 `json:"revision"`
	CreatedAt            string                `json:"created_at"`
	KeyID                string                `json:"key_id"`
	Signature            string                `json:"signature"`
	ExperimentsSignature json.RawMessage       `json:"experiments_signature"`
	Strings              map[string]*wireEntry `json:"strings"`
}

type wireEntry struct {
	Value      *string         `json:"value"`
	Format     string          `json:"format"`
	Experiment json.RawMessage `json:"experiment"`
}

var null = []byte("null")

func parse(data []byte) (b *Bundle, experimentsWellFormed bool, err error) {
	var w wireBundle
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrBundleDecode, err)
	}
	if w.Strings == nil {
		return nil, false, fmt.Errorf("%w: missing strings", ErrBundleDecode)
	}
	b = &Bundle{
		FormatVersion: w.FormatVersion,
		ProjectID:     w.ProjectID,
		Locale:        w.Locale,
		Revision:      w.Revision,
		CreatedAt:     w.CreatedAt,
		KeyID:         w.KeyID,
		Signature:     w.Signature,
		Strings:       make(map[string]Entry, len(w.Strings)),
	}
	_ = json.Unmarshal(w.ExperimentsSignature, &b.ExperimentsSignature)
	experimentsWellFormed = true
	for key, we := range w.Strings {
		if we == nil || we.Value == nil || (we.Format != "text" && we.Format != "icu") {
			return nil, false, fmt.Errorf("%w: invalid entry %q", ErrBundleDecode, key)
		}
		e := Entry{Value: *we.Value, Format: we.Format}
		if len(we.Experiment) > 0 && !bytes.Equal(we.Experiment, null) {
			var x Experiment
			if json.Unmarshal(we.Experiment, &x) != nil || x.Allocation == nil || x.Variants == nil {
				experimentsWellFormed = false
			} else {
				e.Experiment = &x
			}
		}
		b.Strings[key] = e
	}
	return b, experimentsWellFormed, nil
}

func verify(b *Bundle, keys Keys) (ed25519.PublicKey, error) {
	pub, ok := keys[b.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKeyID, b.KeyID)
	}
	sig, ok := decodeSignature(b.Signature)
	if !ok {
		return nil, ErrInvalidSignatureEncoding
	}
	if !ed25519.Verify(pub, SignedContent(b), sig) {
		return nil, ErrSignature
	}
	if b.FormatVersion != 1 {
		return nil, fmt.Errorf("%w %d", ErrUnsupportedFormatVersion, b.FormatVersion)
	}
	return pub, nil
}

func verifyExperiments(b *Bundle, pub ed25519.PublicKey) bool {
	sig, ok := decodeSignature(b.ExperimentsSignature)
	return ok && ed25519.Verify(pub, ExperimentsSignedContent(b), sig)
}

func decodeSignature(s string) ([]byte, bool) {
	sig, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return sig, err == nil && len(sig) == ed25519.SignatureSize
}

func stripExperiments(b *Bundle) {
	b.ExperimentsSignature = ""
	for key, e := range b.Strings {
		if e.Experiment != nil {
			e.Experiment = nil
			b.Strings[key] = e
		}
	}
}
