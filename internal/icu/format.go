package icu

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	textnumber "golang.org/x/text/number"
)

// Locale holds the per-locale plural rules and number printer.
// It is immutable and safe for concurrent use.
type Locale struct {
	tag     language.Tag
	printer *message.Printer
}

func NewLocale(tag language.Tag) *Locale {
	return &Locale{tag: tag, printer: message.NewPrinter(tag)}
}

var forms = [...]string{
	plural.Other: "other",
	plural.Zero:  "zero",
	plural.One:   "one",
	plural.Two:   "two",
	plural.Few:   "few",
	plural.Many:  "many",
}

// Format renders m. A missing argument or a non-numeric value where a number
// is required is an error.
func (m *Message) Format(loc *Locale, args map[string]any) (string, error) {
	if len(m.nodes) == 1 && m.nodes[0].kind == textKind {
		return m.nodes[0].text, nil
	}
	var b strings.Builder
	if err := loc.write(&b, m.nodes, args, nil); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (loc *Locale) write(b *strings.Builder, nodes []node, args map[string]any, hash *num) error {
	for i := range nodes {
		n := &nodes[i]
		switch n.kind {
		case textKind:
			b.WriteString(n.text)
		case poundKind:
			if hash == nil {
				b.WriteByte('#')
			} else {
				b.WriteString(loc.number(*hash, decimalStyle))
			}
		case argKind:
			v, err := arg(args, n.text)
			if err != nil {
				return err
			}
			if x, ok := toNum(v); ok {
				b.WriteString(loc.number(x, decimalStyle))
			} else {
				fmt.Fprint(b, v)
			}
		case numberKind:
			x, err := numArg(args, n.text)
			if err != nil {
				return err
			}
			b.WriteString(loc.number(x, n.style))
		case selectKind:
			v, err := arg(args, n.text)
			if err != nil {
				return err
			}
			msg, ok := n.cases[fmt.Sprint(v)]
			if !ok {
				msg = n.cases["other"]
			}
			if err := loc.write(b, msg, args, hash); err != nil {
				return err
			}
		case pluralKind, ordinalKind:
			x, err := numArg(args, n.text)
			if err != nil {
				return err
			}
			if err := loc.write(b, loc.pluralCase(n, x), args, x.minus(n.offset)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (loc *Locale) pluralCase(n *node, x num) []node {
	for _, e := range n.exact {
		if x.float() == e.value {
			return e.msg
		}
	}
	rules := plural.Cardinal
	if n.kind == ordinalKind {
		rules = plural.Ordinal
	}
	i, v, w, f, t := x.minus(n.offset).operands()
	if msg, ok := n.cases[forms[rules.MatchPlural(loc.tag, i, v, w, f, t)]]; ok {
		return msg
	}
	return n.cases["other"]
}

func (loc *Locale) number(x num, s style) string {
	if x.u != 0 && s != percentStyle {
		return loc.printer.Sprint(textnumber.Decimal(x.u))
	}
	if !x.isFloat {
		switch s {
		case percentStyle:
			return loc.printer.Sprint(textnumber.Percent(x.i))
		default:
			return loc.printer.Sprint(textnumber.Decimal(x.i))
		}
	}
	switch s {
	case integerStyle:
		return loc.printer.Sprint(textnumber.Decimal(math.Round(x.f), textnumber.MaxFractionDigits(0)))
	case percentStyle:
		return loc.printer.Sprint(textnumber.Percent(round(x.f, 2)))
	default:
		return loc.printer.Sprint(textnumber.Decimal(round(x.f, 3)))
	}
}

var errMissing = errors.New("icu: missing argument")

func arg(args map[string]any, name string) (any, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return nil, fmt.Errorf("%w %q", errMissing, name)
	}
	return v, nil
}

func numArg(args map[string]any, name string) (num, error) {
	v, err := arg(args, name)
	if err != nil {
		return num{}, err
	}
	x, ok := toNum(v)
	if !ok {
		return num{}, fmt.Errorf("icu: argument %q is %T, want a number", name, v)
	}
	return x, nil
}

// num is an argument value; integers stay exact. u holds uint64 values above MaxInt64.
type num struct {
	i       int64
	u       uint64
	f       float64
	isFloat bool
}

func toNum(v any) (num, bool) {
	switch x := v.(type) {
	case int:
		return num{i: int64(x)}, true
	case int8:
		return num{i: int64(x)}, true
	case int16:
		return num{i: int64(x)}, true
	case int32:
		return num{i: int64(x)}, true
	case int64:
		return num{i: x}, true
	case uint:
		return fromUint(uint64(x)), true
	case uint8:
		return num{i: int64(x)}, true
	case uint16:
		return num{i: int64(x)}, true
	case uint32:
		return num{i: int64(x)}, true
	case uint64:
		return fromUint(x), true
	case float32:
		return fromFloat(float64(x)), true
	case float64:
		return fromFloat(x), true
	}
	return num{}, false
}

func fromUint(u uint64) num {
	if u > math.MaxInt64 {
		return num{u: u, f: float64(u), isFloat: true}
	}
	return num{i: int64(u)}
}

func fromFloat(f float64) num {
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		return num{i: int64(f)}
	}
	return num{f: f, isFloat: true}
}

func (x num) float() float64 {
	if x.isFloat {
		return x.f
	}
	return float64(x.i)
}

func (x num) minus(offset int64) *num {
	if offset == 0 {
		return &x
	}
	if !x.isFloat && x.i >= math.MinInt64+offset {
		return &num{i: x.i - offset}
	}
	r := fromFloat(x.float() - float64(offset))
	return &r
}

// operands returns the CLDR plural operands i, v, w, f, t, using at most three
// fraction digits like Intl.PluralRules.
func (x num) operands() (i, v, w, f, t int) {
	if !x.isFloat {
		u := uint64(x.i)
		if x.i < 0 {
			u = -u
		}
		return int(u % 1e18), 0, 0, 0, 0
	}
	if x.u != 0 {
		return int(x.u % 1e18), 0, 0, 0, 0
	}
	if math.IsNaN(x.f) || math.IsInf(x.f, 0) {
		return 0, 0, 0, 0, 0
	}
	s := strconv.FormatFloat(round(math.Abs(x.f), 3), 'f', -1, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	if len(intPart) > 18 {
		intPart = intPart[len(intPart)-18:]
	}
	i, _ = strconv.Atoi(intPart)
	if frac != "" {
		f, _ = strconv.Atoi(frac)
	}
	return i, len(frac), len(frac), f, f
}

// round rounds half away from zero to digits fraction digits, like Intl's halfExpand.
func round(f float64, digits int) float64 {
	p := math.Pow10(digits)
	if math.Abs(f) >= 1e15 || math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	return math.Round(f*p) / p
}
