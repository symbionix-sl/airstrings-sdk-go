package airstrings

import (
	"testing"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
)

func benchClient(b *testing.B) *Client {
	b.Helper()
	s := newServer(b)
	bn := textBundle("en", 1, "greeting", "Hello!")
	bn.Strings["items"] = bundle.Entry{Value: "{count, plural, one {# item} other {# items}}", Format: "icu"}
	bn.Strings["checkout.cta"] = bundle.Entry{Value: "Start checkout", Format: "text", Experiment: &bundle.Experiment{
		ID: "exp_checkout_cta", Allocation: fiftyFifty, Variants: map[string]string{"variant_a": "Buy now"},
	}}
	s.publish(bn)
	cfg := s.config()
	cfg.OnExposure = func(Exposure) {}
	c, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = c.Close() })
	if err := c.WaitReady(b.Context()); err != nil || c.Revision("en") != 1 {
		b.Fatalf("not ready: %v", err)
	}
	return c
}

func BenchmarkLocalizerT(b *testing.B) {
	c := benchClient(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Localizer("en", "").T("greeting")
		}
	})
}

func BenchmarkLocalizerTVariant(b *testing.B) {
	c := benchClient(b)
	l := c.Localizer("en", "user_1")
	l.T("checkout.cta")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.T("checkout.cta")
		}
	})
}

func BenchmarkLocalizerFormat(b *testing.B) {
	c := benchClient(b)
	args := map[string]any{"count": 1234}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Localizer("en", "").Format("items", args)
		}
	})
}
