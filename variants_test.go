package airstrings

import (
	"encoding/json"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
)

type exposureLog struct {
	mu  sync.Mutex
	got []Exposure
}

func (l *exposureLog) add(e Exposure) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, e)
}

func (l *exposureLog) all() []Exposure {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Exposure(nil), l.got...)
}

// settle waits long enough for asynchronous exposure delivery.
func settle() { time.Sleep(50 * time.Millisecond) }

func checkoutBundle(locale string, allocation map[string]int64) *bundle.Bundle {
	b := textBundle(locale, 1, "plain", "Plain")
	b.Strings["checkout.cta"] = bundle.Entry{Value: "Start checkout", Format: "text", Experiment: &bundle.Experiment{
		ID:         "exp_checkout_cta",
		Allocation: allocation,
		Variants:   map[string]string{"variant_a": "Buy now"},
	}}
	return b
}

var fiftyFifty = map[string]int64{"control": 50, "variant_a": 50}

func startWithExposures(t *testing.T, s *server, locales ...string) (*Client, *exposureLog) {
	t.Helper()
	exposures := &exposureLog{}
	cfg := s.config()
	if len(locales) > 0 {
		cfg.Locales = locales
	}
	cfg.OnExposure = exposures.add
	c, _ := start(t, cfg)
	return c, exposures
}

func TestVariantServedWithAssignment(t *testing.T) {
	s := newServer(t)
	s.publish(checkoutBundle("en", fiftyFifty))
	c, exposures := startWithExposures(t, s)

	if got := c.Localizer("en", "user_1").T("checkout.cta"); got != "Buy now" {
		t.Fatalf("user_1 T = %q", got)
	}
	if got := c.Localizer("en", "user_2").Format("checkout.cta", nil); got != "Start checkout" {
		t.Fatalf("user_2 Format = %q", got)
	}
	if got := c.Localizer("en", "user_1").T("plain"); got != "Plain" {
		t.Fatalf("plain = %q", got)
	}
	eventually(t, func() bool { return len(exposures.all()) == 2 })
	want := map[Exposure]bool{
		{Key: "checkout.cta", ExperimentID: "exp_checkout_cta", Variant: "variant_a", Locale: "en", AssignmentID: "user_1"}: true,
		{Key: "checkout.cta", ExperimentID: "exp_checkout_cta", Variant: "control", Locale: "en", AssignmentID: "user_2"}:   true,
	}
	for _, e := range exposures.all() {
		if !want[e] {
			t.Fatalf("unexpected exposure %+v", e)
		}
	}
}

func TestNoAssignmentServesBaseWithoutExposure(t *testing.T) {
	s := newServer(t)
	s.publish(checkoutBundle("en", fiftyFifty))
	c, exposures := startWithExposures(t, s)
	if got := c.Localizer("en", "").T("checkout.cta"); got != "Start checkout" {
		t.Fatalf("T = %q", got)
	}
	settle()
	if len(exposures.all()) != 0 {
		t.Fatalf("exposures = %v", exposures.all())
	}
}

func TestExposureDedupe(t *testing.T) {
	s := newServer(t)
	s.publish(checkoutBundle("en", fiftyFifty))
	s.publish(checkoutBundle("it", fiftyFifty))
	c, exposures := startWithExposures(t, s, "en", "it")
	for range 5 {
		c.Localizer("en", "user_1").T("checkout.cta")
		c.Localizer("it", "user_1").Format("checkout.cta", nil)
	}
	settle()
	if got := exposures.all(); len(got) != 1 {
		t.Fatalf("exposures = %+v, want exactly one across reads and locales", got)
	}
	if err := c.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.Localizer("en", "user_1").T("checkout.cta")
	c.Localizer("en", "user_3").T("checkout.cta")
	settle()
	if got := exposures.all(); len(got) != 2 || got[1].AssignmentID != "user_3" {
		t.Fatalf("exposures = %+v", got)
	}
}

func TestExposureDedupeResetsAtCapacity(t *testing.T) {
	s := newServer(t)
	s.publish(checkoutBundle("en", fiftyFifty))
	c, exposures := startWithExposures(t, s)
	c.exposures.limit = 2
	for _, id := range []string{"a", "b", "c", "a"} {
		c.Localizer("en", id).T("checkout.cta")
	}
	settle()
	if got := len(exposures.all()); got != 4 {
		t.Fatalf("exposures = %d, want 4 (set reset after exceeding the cap)", got)
	}
}

func TestExposureIsAsynchronous(t *testing.T) {
	s := newServer(t)
	s.publish(checkoutBundle("en", fiftyFifty))
	release := make(chan struct{})
	delivered := make(chan Exposure, 1)
	cfg := s.config()
	cfg.OnExposure = func(e Exposure) {
		<-release
		delivered <- e
	}
	c, _ := start(t, cfg)
	done := make(chan string)
	go func() { done <- c.Localizer("en", "user_1").T("checkout.cta") }()
	select {
	case got := <-done:
		if got != "Buy now" {
			t.Fatalf("T = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read blocked on exposure handler")
	}
	close(release)
	if e := <-delivered; e.Variant != "variant_a" {
		t.Fatalf("exposure = %+v", e)
	}
}

func TestInvalidExperimentsServeBase(t *testing.T) {
	cases := map[string]func(s *server) []byte{
		"allocation sum not 100": func(s *server) []byte {
			return s.key.Sign(checkoutBundle("en", map[string]int64{"control": 40, "variant_a": 40}))
		},
		"missing variant value": func(s *server) []byte {
			b := checkoutBundle("en", fiftyFifty)
			b.Strings["checkout.cta"].Experiment.Variants = map[string]string{}
			return s.key.Sign(b)
		},
		"tampered experiments_signature": func(s *server) []byte {
			var m map[string]any
			_ = json.Unmarshal(s.key.Sign(checkoutBundle("en", fiftyFifty)), &m)
			m["strings"].(map[string]any)["checkout.cta"].(map[string]any)["experiment"].(map[string]any)["variants"] = map[string]any{"variant_a": "Evil"}
			data, _ := json.Marshal(m)
			return data
		},
		"absent experiments_signature": func(s *server) []byte {
			var m map[string]any
			_ = json.Unmarshal(s.key.Sign(checkoutBundle("en", fiftyFifty)), &m)
			delete(m, "experiments_signature")
			data, _ := json.Marshal(m)
			return data
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			s := newServer(t)
			s.publishRaw("en", build(s))
			c, exposures := startWithExposures(t, s)
			if c.Revision("en") != 1 {
				t.Fatal("bundle not accepted")
			}
			if got := c.Localizer("en", "user_1").T("checkout.cta"); got != "Start checkout" {
				t.Fatalf("T = %q", got)
			}
			settle()
			if len(exposures.all()) != 0 {
				t.Fatalf("exposures = %v", exposures.all())
			}
		})
	}
}

func TestVariantsFromSeed(t *testing.T) {
	s := newServer(t)
	exposures := &exposureLog{}
	cfg := seedConfig(s, fstest.MapFS{"en.json": {Data: s.key.Sign(checkoutBundle("en", fiftyFifty))}})
	cfg.OnExposure = exposures.add
	c, _ := start(t, cfg)
	if got := c.Localizer("en", "user_1").T("checkout.cta"); got != "Buy now" {
		t.Fatalf("T = %q", got)
	}
	eventually(t, func() bool { return len(exposures.all()) == 1 })
}

func TestICUVariant(t *testing.T) {
	s := newServer(t)
	b := textBundle("en", 1)
	b.Strings["items"] = bundle.Entry{Value: "{n, plural, one {# item} other {# items}}", Format: "icu", Experiment: &bundle.Experiment{
		ID:         "exp_checkout_cta",
		Allocation: fiftyFifty,
		Variants:   map[string]string{"variant_a": "{n, plural, one {# thing} other {# things}}"},
	}}
	s.publish(b)
	c, _ := start(t, s.config())
	if got := c.Localizer("en", "user_1").Format("items", map[string]any{"n": 2}); got != "2 things" {
		t.Fatalf("variant Format = %q", got)
	}
	if got := c.Localizer("en", "user_2").Format("items", map[string]any{"n": 2}); got != "2 items" {
		t.Fatalf("control Format = %q", got)
	}
}
