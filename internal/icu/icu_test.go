package icu_test

import (
	"math"
	"testing"

	"golang.org/x/text/language"

	"github.com/symbionix-sl/airstrings-sdk-go/internal/icu"
)

type args = map[string]any

func format(t *testing.T, locale, pattern string, a args) string {
	t.Helper()
	m, err := icu.Compile(pattern)
	if err != nil {
		t.Fatalf("Compile(%q): %v", pattern, err)
	}
	s, err := m.Format(icu.NewLocale(language.Make(locale)), a)
	if err != nil {
		t.Fatalf("Format(%q, %v): %v", pattern, a, err)
	}
	return s
}

func TestFormat(t *testing.T) {
	const (
		items   = "{count, plural, =0 {no items} one {# item} other {# items}}"
		guests  = "{n, plural, offset:1 =0 {nobody} =1 {just {name}} one {{name} and # other} other {{name} and # others}}"
		ruBooks = "{n, plural, one {# книга} few {# книги} many {# книг} other {# книги}}"
		arAll   = "{n, plural, zero {zero} one {one} two {two} few {few} many {many} other {other}}"
		plFiles = "{n, plural, one {# plik} few {# pliki} many {# plików} other {# pliku}}"
		gender  = "{g, select, male {He} female {She} other {They}}"
		ordinal = "{n, selectordinal, one {#st} two {#nd} few {#rd} other {#th}}"
	)
	cases := []struct {
		locale, pattern string
		args            args
		want            string
	}{
		{"en", "Hello", nil, "Hello"},
		{"en", "", nil, ""},
		{"en", "Hello, {name}!", args{"name": "Ada"}, "Hello, Ada!"},
		{"en", "{ name }", args{"name": "Ada"}, "Ada"},
		{"en", "{a}{b}", args{"a": true, "b": 'x'}, "true120"},
		{"en", "{n}", args{"n": 1234567}, "1,234,567"},
		{"de", "{n}", args{"n": 1234567}, "1.234.567"},
		{"fr", "{n}", args{"n": 1234.5}, "1\u00a0234,5"},
		{"en", "{n, number}", args{"n": 1234.5678}, "1,234.568"},
		{"en", "{n, number}", args{"n": -0.5}, "-0.5"},
		{"en", "{n, number}", args{"n": uint64(math.MaxUint64)}, "18,446,744,073,709,551,615"},
		{"en", "{n, number}", args{"n": int8(-7)}, "-7"},
		{"en", "{n, number}", args{"n": float32(2.5)}, "2.5"},
		{"en", "{n, number, integer}", args{"n": 2.5}, "3"},
		{"en", "{n, number, integer}", args{"n": 1234.4}, "1,234"},
		{"en", "{n, number, integer}", args{"n": 42}, "42"},
		{"en", "{p, number, percent}", args{"p": 0.256}, "26%"},
		{"de", "{p, number, percent}", args{"p": 0.5}, "50\u00a0%"},
		{"en", "{p, number, percent}", args{"p": 1}, "100%"},
		{"en", items, args{"count": 0}, "no items"},
		{"en", items, args{"count": 1}, "1 item"},
		{"en", items, args{"count": 2}, "2 items"},
		{"en", items, args{"count": 1000}, "1,000 items"},
		{"en", items, args{"count": 1.5}, "1.5 items"},
		{"en", items, args{"count": 1.0}, "1 item"},
		{"en", items, args{"count": int64(-1)}, "-1 item"},
		{"de", items, args{"count": 1000}, "1.000 items"},
		{"en", guests, args{"n": 0, "name": "Ada"}, "nobody"},
		{"en", guests, args{"n": 1, "name": "Ada"}, "just Ada"},
		{"en", guests, args{"n": 2, "name": "Ada"}, "Ada and 1 other"},
		{"en", guests, args{"n": 3, "name": "Ada"}, "Ada and 2 others"},
		{"ru", ruBooks, args{"n": 1}, "1 книга"},
		{"ru", ruBooks, args{"n": 3}, "3 книги"},
		{"ru", ruBooks, args{"n": 5}, "5 книг"},
		{"ru", ruBooks, args{"n": 21}, "21 книга"},
		{"ru", ruBooks, args{"n": 1.5}, "1,5 книги"},
		{"ar", arAll, args{"n": 0}, "zero"},
		{"ar", arAll, args{"n": 1}, "one"},
		{"ar", arAll, args{"n": 2}, "two"},
		{"ar", arAll, args{"n": 3}, "few"},
		{"ar", arAll, args{"n": 11}, "many"},
		{"ar", arAll, args{"n": 100}, "other"},
		{"pl", plFiles, args{"n": 1}, "1 plik"},
		{"pl", plFiles, args{"n": 2}, "2 pliki"},
		{"pl", plFiles, args{"n": 5}, "5 plików"},
		{"pl", plFiles, args{"n": 12}, "12 plików"},
		{"pl", plFiles, args{"n": 22}, "22 pliki"},
		{"pl", plFiles, args{"n": 1.5}, "1,5 pliku"},
		{"en-US", items, args{"count": 1}, "1 item"},
		{"pt-BR", "{n, plural, one {um} other {outros}}", args{"n": 0}, "um"},
		{"en", "{n, plural, one {one} other {other}}", args{"n": math.NaN()}, "other"},
		{"en", gender, args{"g": "female"}, "She"},
		{"en", gender, args{"g": "unknown"}, "They"},
		{"en", "{v, select, 3 {three} other {?}}", args{"v": 3}, "three"},
		{"en", ordinal, args{"n": 1}, "1st"},
		{"en", ordinal, args{"n": 2}, "2nd"},
		{"en", ordinal, args{"n": 3}, "3rd"},
		{"en", ordinal, args{"n": 4}, "4th"},
		{"en", ordinal, args{"n": 11}, "11th"},
		{"en", ordinal, args{"n": 21}, "21st"},
		{"en", ordinal, args{"n": 112}, "112th"},
		{"en", "{g, select, female {{n, plural, one {She has # cat} other {She has # cats}}} other {{n, plural, one {They have # cat} other {They have # cats}}}}", args{"g": "female", "n": 2}, "She has 2 cats"},
		{"en", "{n, plural, other {{g, select, other {# stays literal in select}}}}", args{"n": 2, "g": "x"}, "# stays literal in select"},
		{"en", "It''s {name}''s", args{"name": "Ada"}, "It's Ada's"},
		{"en", "I'm {name}", args{"name": "Ada"}, "I'm Ada"},
		{"en", "'{name}'", nil, "{name}"},
		{"en", "'{a''b}' x", nil, "{a'b} x"},
		{"en", "'}'", nil, "}"},
		{"en", "'|'", nil, "|"},
		{"en", "'{unterminated", nil, "{unterminated"},
		{"en", "'#' and #", nil, "'#' and #"},
		{"en", "{n, plural, other {'#' is #}}", args{"n": 3}, "# is 3"},
		{"en", "trailing '", nil, "trailing '"},
		{"en", "a}b", nil, "a}b"},
		{"en", "{n, plural, offset:2 other {#}}", args{"n": 1.5}, "-0.5"},
	}
	for _, tc := range cases {
		if got := format(t, tc.locale, tc.pattern, tc.args); got != tc.want {
			t.Errorf("[%s] %q %v = %q, want %q", tc.locale, tc.pattern, tc.args, got, tc.want)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	for _, p := range []string{
		"{",
		"{name",
		"{}",
		"{, plural}",
		"{name, }",
		"{name, foo}",
		"{d, date}",
		"{d, date, short}",
		"{t, time}",
		"{n, spellout}",
		"{n, ordinal}",
		"{n, duration}",
		"{n, choice, 0#none|1#one}",
		"{n, number, currency}",
		"{n, number, ::currency/EUR}",
		"{n, number, integer",
		"{n, plural, one {x}}",
		"{n, plural, one {x} one {y} other {z}}",
		"{n, plural, =1 {x} =1 {y} other {z}}",
		"{n, plural other {x}}",
		"{n, plural, offset:x other {x}}",
		"{n, plural, other {x}",
		"{n, plural, other x}",
		"{n, plural, = {x} other {y}}",
		"{n, select, a {x}}",
		"{n, select, other {x}",
		"{n, selectordinal, offset:1 other {#}}",
		"{n, plural, other {{unclosed}}",
	} {
		if _, err := icu.Compile(p); err == nil {
			t.Errorf("Compile(%q) succeeded", p)
		}
	}
}

func TestFormatErrors(t *testing.T) {
	en := icu.NewLocale(language.English)
	cases := []struct {
		pattern string
		args    args
	}{
		{"{name}", nil},
		{"{name}", args{"other": 1}},
		{"{name}", args{"name": nil}},
		{"{n, number}", args{"n": "12"}},
		{"{n, number, percent}", args{"n": struct{}{}}},
		{"{n, plural, other {#}}", args{"n": "3"}},
		{"{n, selectordinal, other {#}}", args{}},
		{"{g, select, other {x}}", nil},
		{"{n, plural, one {{missing}} other {ok}}", args{"n": 1}},
	}
	for _, tc := range cases {
		m, err := icu.Compile(tc.pattern)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.pattern, err)
		}
		if s, err := m.Format(en, tc.args); err == nil {
			t.Errorf("Format(%q, %v) = %q, want error", tc.pattern, tc.args, s)
		}
	}
}

func BenchmarkCompile(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = icu.Compile("{n, plural, offset:1 =0 {nobody} one {{name} and # other} other {{name} and # others}}")
	}
}

func BenchmarkFormatPlural(b *testing.B) {
	m, _ := icu.Compile("{count, plural, =0 {no items} one {# item} other {# items}}")
	en := icu.NewLocale(language.English)
	a := args{"count": 1234}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = m.Format(en, a)
	}
}

func BenchmarkFormatSimpleArg(b *testing.B) {
	m, _ := icu.Compile("Hello, {name}!")
	en := icu.NewLocale(language.English)
	a := args{"name": "Ada"}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = m.Format(en, a)
	}
}
