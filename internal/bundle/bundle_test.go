package bundle_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundletest"
)

const project = "proj_test12345678"

func keysOf(t *testing.T, ks ...bundletest.Key) bundle.Keys {
	t.Helper()
	ids := make([]string, len(ks))
	for i, k := range ks {
		ids[i] = k.ID
	}
	keys, err := bundle.ParseKeys(ids)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestSignedContentMatchesContractFixture(t *testing.T) {
	b := &bundle.Bundle{
		FormatVersion: 1,
		ProjectID:     "proj_a1b2c3d4e5f6",
		Locale:        "en-US",
		Revision:      42,
		CreatedAt:     "2026-02-25T14:30:00Z",
		KeyID:         "BASE64_PUBLIC_KEY",
		Signature:     "dummy",
		Strings: map[string]bundle.Entry{
			"onboarding.welcome_title": {Value: "Welcome to Acme", Format: "text"},
			"onboarding.welcome_body":  {Value: "Get started in minutes.", Format: "text"},
			"settings.language":        {Value: "Language", Format: "text"},
			"items.count":              {Value: "{count, plural, one {# item} other {# items}}", Format: "icu"},
			"error.network":            {Value: "Something went wrong. Please try again.", Format: "text"},
		},
	}
	want := `{"format_version":1,"project_id":"proj_a1b2c3d4e5f6","locale":"en-US","revision":42,"created_at":"2026-02-25T14:30:00Z","strings":{"error.network":{"format":"text","value":"Something went wrong. Please try again."},"items.count":{"format":"icu","value":"{count, plural, one {# item} other {# items}}"},"onboarding.welcome_body":{"format":"text","value":"Get started in minutes."},"onboarding.welcome_title":{"format":"text","value":"Welcome to Acme"},"settings.language":{"format":"text","value":"Language"}}}`
	if got := string(bundle.SignedContent(b)); got != want {
		t.Fatalf("canonical mismatch\n got %s\nwant %s", got, want)
	}
}

func TestExperimentsSignedContentMatchesContractFixture(t *testing.T) {
	b := &bundle.Bundle{
		FormatVersion: 1,
		ProjectID:     "proj_x",
		Locale:        "en-US",
		Revision:      42,
		CreatedAt:     "2026-07-14T10:00:00Z",
		Strings: map[string]bundle.Entry{
			"checkout.cta": {Value: "Continue", Format: "text", Experiment: &bundle.Experiment{
				ID:         "exp_a1b2c3d4e5f6",
				Allocation: map[string]int64{"variant_a": 50, "control": 50},
				Variants:   map[string]string{"variant_a": "Continue"},
			}},
			"plain": {Value: "No experiment", Format: "text"},
		},
	}
	want := `{"format_version":1,"project_id":"proj_x","locale":"en-US","revision":42,"created_at":"2026-07-14T10:00:00Z","experiments":{"checkout.cta":{"allocation":{"control":50,"variant_a":50},"id":"exp_a1b2c3d4e5f6","variants":{"variant_a":"Continue"}}}}`
	if got := string(bundle.ExperimentsSignedContent(b)); got != want {
		t.Fatalf("canonical mismatch\n got %s\nwant %s", got, want)
	}
}

func TestSignedContentExcludesExperiments(t *testing.T) {
	b := bundletest.New(project, "en", 1, map[string]string{"cta": "Buy"})
	before := string(bundle.SignedContent(b))
	b.Strings["cta"] = bundle.Entry{Value: "Buy", Format: "text", Experiment: &bundle.Experiment{
		ID: "exp_1", Allocation: map[string]int64{"control": 100}, Variants: map[string]string{},
	}}
	if after := string(bundle.SignedContent(b)); after != before {
		t.Fatalf("experiment changed main canonical content:\n%s\n%s", before, after)
	}
}

func TestSignedContentEscaping(t *testing.T) {
	b := bundletest.New(project, "ja", 7, map[string]string{
		"z": "line1\nline2\ttab \"quoted\" back\\slash",
		"a": "before\x01after\x1f\b\f\r",
		"m": "こんにちは </script> & 🙂",
	})
	want := `{"format_version":1,"project_id":"proj_test12345678","locale":"ja","revision":7,"created_at":"2026-02-25T14:30:00Z","strings":{"a":{"format":"text","value":"before\u0001after\u001f\b\f\r"},"m":{"format":"text","value":"こんにちは </script> & 🙂"},"z":{"format":"text","value":"line1\nline2\ttab \"quoted\" back\\slash"}}}`
	if got := string(bundle.SignedContent(b)); got != want {
		t.Fatalf("canonical mismatch\n got %s\nwant %s", got, want)
	}
}

func TestSignedContentEmptyStrings(t *testing.T) {
	b := bundletest.New(project, "en", 100, nil)
	if got := string(bundle.SignedContent(b)); !strings.HasSuffix(got, `"revision":100,"created_at":"2026-02-25T14:30:00Z","strings":{}}`) {
		t.Fatalf("got %s", got)
	}
}

func TestOpenAcceptsValidBundle(t *testing.T) {
	k := bundletest.NewKey()
	b := bundletest.New(project, "en", 3, map[string]string{"greeting": "Hello!"})
	b.Strings["items.count"] = bundle.Entry{Value: "{n, plural, one {# item} other {# items}}", Format: "icu"}
	got, err := bundle.Open(k.Sign(b), keysOf(t, bundletest.NewKey(), k), project, "en")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 3 || got.Strings["greeting"].Value != "Hello!" || got.Strings["items.count"].Format != "icu" {
		t.Fatalf("unexpected bundle %+v", got)
	}
}

func TestOpenRejects(t *testing.T) {
	k := bundletest.NewKey()
	other := bundletest.NewKey()
	valid := func() *bundle.Bundle {
		return bundletest.New(project, "en", 1, map[string]string{"greeting": "Hello!"})
	}
	signed := func(mutate func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(k.Sign(valid()), &m); err != nil {
			t.Fatal(err)
		}
		mutate(m)
		data, _ := json.Marshal(m)
		return data
	}

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"tampered strings", signed(func(m map[string]any) {
			m["strings"].(map[string]any)["greeting"].(map[string]any)["value"] = "Hacked"
		}), bundle.ErrSignature},
		{"tampered revision", signed(func(m map[string]any) { m["revision"] = 2 }), bundle.ErrSignature},
		{"signed by another key claiming a configured key_id", func() []byte {
			data := other.Sign(valid())
			return []byte(strings.Replace(string(data), other.ID, k.ID, 1))
		}(), bundle.ErrSignature},
		{"unknown key_id", other.Sign(valid()), bundle.ErrUnknownKeyID},
		{"signature not base64url", signed(func(m map[string]any) { m["signature"] = "!!!" }), bundle.ErrInvalidSignatureEncoding},
		{"signature wrong length", signed(func(m map[string]any) {
			m["signature"] = base64.RawURLEncoding.EncodeToString(make([]byte, 63))
		}), bundle.ErrInvalidSignatureEncoding},
		{"signature with padding", signed(func(m map[string]any) { m["signature"] = m["signature"].(string) + "==" }), bundle.ErrInvalidSignatureEncoding},
		{"unsupported format_version", func() []byte {
			b := valid()
			b.FormatVersion = 2
			return k.Sign(b)
		}(), bundle.ErrUnsupportedFormatVersion},
		{"not json", []byte("{"), bundle.ErrBundleDecode},
		{"json array", []byte("[]"), bundle.ErrBundleDecode},
		{"missing strings", signed(func(m map[string]any) { delete(m, "strings") }), bundle.ErrBundleDecode},
		{"null entry", signed(func(m map[string]any) { m["strings"].(map[string]any)["greeting"] = nil }), bundle.ErrBundleDecode},
		{"missing value", signed(func(m map[string]any) {
			delete(m["strings"].(map[string]any)["greeting"].(map[string]any), "value")
		}), bundle.ErrBundleDecode},
		{"unknown format", signed(func(m map[string]any) {
			m["strings"].(map[string]any)["greeting"].(map[string]any)["format"] = "html"
		}), bundle.ErrBundleDecode},
		{"fractional revision", signed(func(m map[string]any) { m["revision"] = 1.5 }), bundle.ErrBundleDecode},
		{"wrong project", func() []byte {
			b := valid()
			b.ProjectID = "proj_other000000"
			return k.Sign(b)
		}(), bundle.ErrProjectMismatch},
		{"wrong locale", func() []byte {
			b := valid()
			b.Locale = "ja"
			return k.Sign(b)
		}(), bundle.ErrLocaleMismatch},
	}
	keys := keysOf(t, k)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bundle.Open(tc.data, keys, project, "en")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got != nil {
				t.Fatal("rejected bundle returned")
			}
		})
	}
}

func TestOpenIgnoresUnknownTopLevelFields(t *testing.T) {
	k := bundletest.NewKey()
	data := k.Sign(bundletest.New(project, "en", 1, map[string]string{"a": "A"}))
	data = []byte(strings.Replace(string(data), `{`, `{"future_field":{"x":[1,2]},`, 1))
	if _, err := bundle.Open(data, keysOf(t, k), project, "en"); err != nil {
		t.Fatal(err)
	}
}

func TestParseKeys(t *testing.T) {
	good := bundletest.NewKey().ID
	if _, err := bundle.ParseKeys([]string{good}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))} {
		if _, err := bundle.ParseKeys([]string{good, bad}); !errors.Is(err, bundle.ErrInvalidKeyIDEncoding) {
			t.Errorf("ParseKeys(%q) err = %v", bad, err)
		}
	}
	if _, err := bundle.ParseKeys(nil); !errors.Is(err, bundle.ErrInvalidKeyIDEncoding) {
		t.Errorf("ParseKeys(nil) err = %v", err)
	}
}

func experimentBundle() *bundle.Bundle {
	b := bundletest.New(project, "en", 1, map[string]string{"plain": "Plain"})
	b.Strings["cta"] = bundle.Entry{Value: "Base", Format: "text", Experiment: &bundle.Experiment{
		ID:         "exp_checkout_cta",
		Allocation: map[string]int64{"control": 50, "variant_a": 50},
		Variants:   map[string]string{"variant_a": "Variant A"},
	}}
	return b
}

func TestOpenKeepsVerifiedExperiments(t *testing.T) {
	k := bundletest.NewKey()
	got, err := bundle.Open(k.Sign(experimentBundle()), keysOf(t, k), project, "en")
	if err != nil {
		t.Fatal(err)
	}
	e := got.Strings["cta"].Experiment
	if e == nil || e.ID != "exp_checkout_cta" || e.Allocation["variant_a"] != 50 || e.Variants["variant_a"] != "Variant A" {
		t.Fatalf("experiment = %+v", e)
	}
}

func TestOpenStripsUntrustedExperiments(t *testing.T) {
	k := bundletest.NewKey()
	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(k.Sign(experimentBundle()), &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		data, _ := json.Marshal(m)
		return data
	}
	exp := func(m map[string]any) map[string]any {
		return m["strings"].(map[string]any)["cta"].(map[string]any)["experiment"].(map[string]any)
	}
	cases := map[string][]byte{
		"tampered allocation":         mutate(func(m map[string]any) { exp(m)["allocation"] = map[string]any{"control": 0, "variant_a": 100} }),
		"tampered variant":            mutate(func(m map[string]any) { exp(m)["variants"] = map[string]any{"variant_a": "Evil"} }),
		"missing signature":           mutate(func(m map[string]any) { delete(m, "experiments_signature") }),
		"non-string signature":        mutate(func(m map[string]any) { m["experiments_signature"] = 12345 }),
		"bad signature encoding":      mutate(func(m map[string]any) { m["experiments_signature"] = "%%%" }),
		"malformed block (string)":    mutate(func(m map[string]any) { m["strings"].(map[string]any)["cta"].(map[string]any)["experiment"] = "nope" }),
		"malformed block (fraction)":  mutate(func(m map[string]any) { exp(m)["allocation"] = map[string]any{"control": 50.5, "variant_a": 49.5} }),
		"malformed block (no fields)": mutate(func(m map[string]any) { exp(m)["variants"] = nil }),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := bundle.Open(data, keysOf(t, k), project, "en")
			if err != nil {
				t.Fatalf("bundle rejected: %v", err)
			}
			if got.Strings["cta"].Value != "Base" || got.Strings["plain"].Value != "Plain" {
				t.Fatalf("base values lost: %+v", got.Strings)
			}
			for key, e := range got.Strings {
				if e.Experiment != nil {
					t.Fatalf("untrusted experiment kept on %q", key)
				}
			}
		})
	}
}
