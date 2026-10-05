// Package bundletest builds signed bundles for tests.
package bundletest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
)

type Key struct {
	ID   string
	priv ed25519.PrivateKey
}

func NewKey() Key {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return Key{ID: base64.StdEncoding.EncodeToString(pub), priv: priv}
}

// New returns an unsigned bundle with sensible defaults and the given text strings.
func New(projectID, locale string, revision int64, strings map[string]string) *bundle.Bundle {
	b := &bundle.Bundle{
		FormatVersion: 1,
		ProjectID:     projectID,
		Locale:        locale,
		Revision:      revision,
		CreatedAt:     "2026-02-25T14:30:00Z",
		Strings:       make(map[string]bundle.Entry, len(strings)),
	}
	for k, v := range strings {
		b.Strings[k] = bundle.Entry{Value: v, Format: "text"}
	}
	return b
}

// Sign signs b in place (main and, when present, experiments signature) and returns its JSON.
func (k Key) Sign(b *bundle.Bundle) []byte {
	b.KeyID = k.ID
	b.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, bundle.SignedContent(b)))
	b.ExperimentsSignature = ""
	for _, e := range b.Strings {
		if e.Experiment != nil {
			b.ExperimentsSignature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, bundle.ExperimentsSignedContent(b)))
			break
		}
	}
	return Marshal(b)
}

func Marshal(b *bundle.Bundle) []byte {
	data, err := json.Marshal(b)
	if err != nil {
		panic(err)
	}
	return data
}
