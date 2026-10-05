package bundle_test

import (
	"testing"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
)

var vectors = []struct {
	experimentID, assignmentID string
	allocation                 map[string]int64
	variants                   map[string]string
	bucket                     int
	variant                    string
}{
	{"exp_checkout_cta", "user_1", map[string]int64{"control": 50, "variant_a": 50}, map[string]string{"variant_a": "Variant A copy"}, 78, "variant_a"},
	{"exp_checkout_cta", "user_2", map[string]int64{"control": 50, "variant_a": 50}, map[string]string{"variant_a": "Variant A copy"}, 19, "control"},
	{"exp_paywall_title", "user_2", map[string]int64{"control": 34, "variant_a": 33, "variant_b": 33}, map[string]string{"variant_a": "A title", "variant_b": "B title"}, 50, "variant_a"},
	{"exp_paywall_title", "device-9f8e7d", map[string]int64{"control": 34, "variant_a": 33, "variant_b": 33}, map[string]string{"variant_a": "A title", "variant_b": "B title"}, 78, "variant_b"},
	{"exp_unicode", "ユーザー_1", map[string]int64{"control": 50, "variant_a": 50}, map[string]string{"variant_a": "A copy"}, 97, "variant_a"},
	{"exp_edge", "u", map[string]int64{"a_variant": 10, "control": 90}, map[string]string{"a_variant": "AV copy"}, 15, "control"},
}

func TestBucketVectors(t *testing.T) {
	for _, v := range vectors {
		if got := bundle.Bucket(v.experimentID, v.assignmentID); got != v.bucket {
			t.Errorf("Bucket(%q, %q) = %d, want %d", v.experimentID, v.assignmentID, got, v.bucket)
		}
	}
}

func TestSelectorVectors(t *testing.T) {
	for _, v := range vectors {
		s := bundle.NewSelector(&bundle.Experiment{ID: v.experimentID, Allocation: v.allocation, Variants: v.variants}, "BASE")
		if s == nil {
			t.Fatalf("%s: valid experiment rejected", v.experimentID)
		}
		c := s.Choices[s.Pick(v.assignmentID)]
		want := v.variants[v.variant]
		if v.variant == "control" {
			want = "BASE"
		}
		if !c.OK || c.Variant != v.variant || c.Value != want {
			t.Errorf("%s/%s picked %+v, want %s=%q", v.experimentID, v.assignmentID, c, v.variant, want)
		}
	}
}

func TestSelectorRejectsInvalidExperiments(t *testing.T) {
	cases := map[string]*bundle.Experiment{
		"sum below 100":   {ID: "exp_x", Allocation: map[string]int64{"control": 40, "variant_a": 40}, Variants: map[string]string{"variant_a": "A"}},
		"sum above 100":   {ID: "exp_x", Allocation: map[string]int64{"control": 60, "variant_a": 60}, Variants: map[string]string{"variant_a": "A"}},
		"negative weight": {ID: "exp_x", Allocation: map[string]int64{"control": 110, "variant_a": -10}, Variants: map[string]string{"variant_a": "A"}},
		"empty id":        {ID: "", Allocation: map[string]int64{"control": 50, "variant_a": 50}, Variants: map[string]string{"variant_a": "A"}},
		"no allocation":   {ID: "exp_x"},
		"overflowing sum": {ID: "exp_x", Allocation: map[string]int64{"control": 1 << 62, "variant_a": 1 << 62, "variant_b": 1 << 62, "variant_c": 1<<62 + 100}},
	}
	for name, e := range cases {
		if s := bundle.NewSelector(e, "BASE"); s != nil {
			t.Errorf("%s: selector built for invalid experiment", name)
		}
	}
	if s := bundle.NewSelector(nil, "BASE"); s != nil {
		t.Error("selector built for nil experiment")
	}
}

func TestSelectorMissingVariantValueIsNotOK(t *testing.T) {
	s := bundle.NewSelector(&bundle.Experiment{ID: "exp_checkout_cta", Allocation: map[string]int64{"control": 50, "variant_a": 50}}, "BASE")
	if s == nil {
		t.Fatal("selector rejected")
	}
	if c := s.Choices[s.Pick("user_1")]; c.OK {
		t.Fatalf("choice with missing value is OK: %+v", c)
	}
	if c := s.Choices[s.Pick("user_2")]; !c.OK || c.Variant != "control" || c.Value != "BASE" {
		t.Fatalf("control choice = %+v", c)
	}
}

func TestSelectorZeroWeightNeverPicked(t *testing.T) {
	s := bundle.NewSelector(&bundle.Experiment{ID: "exp_z", Allocation: map[string]int64{"a": 0, "control": 100}, Variants: map[string]string{"a": "A"}}, "BASE")
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		if c := s.Choices[s.Pick(id)]; c.Variant != "control" {
			t.Fatalf("picked %q", c.Variant)
		}
	}
}

func BenchmarkSelectorPick(b *testing.B) {
	s := bundle.NewSelector(&bundle.Experiment{ID: "exp_paywall_title", Allocation: map[string]int64{"control": 34, "variant_a": 33, "variant_b": 33}, Variants: map[string]string{"variant_a": "A", "variant_b": "B"}}, "BASE")
	b.ReportAllocs()
	for b.Loop() {
		s.Pick("device-9f8e7d")
	}
}
