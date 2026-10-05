// Package icu implements ICU MessageFormat v1: simple and number arguments,
// plural, selectordinal and select, with CLDR plural rules and locale number
// formatting. Date, time, skeleton and spellout arguments are unsupported.
package icu

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type kind uint8

const (
	textKind kind = iota
	argKind
	numberKind
	pluralKind
	ordinalKind
	selectKind
	poundKind
)

type style uint8

const (
	decimalStyle style = iota
	integerStyle
	percentStyle
)

type node struct {
	kind   kind
	text   string // literal text or argument name
	style  style
	offset int64
	exact  []exactCase
	cases  map[string][]node
}

type exactCase struct {
	value float64
	msg   []node
}

// Message is a compiled pattern. It is immutable and safe for concurrent use.
type Message struct {
	nodes []node
}

// Compile parses pattern using ICU's default apostrophe mode: a doubled apostrophe is a literal
// apostrophe and a single apostrophe starts quoted text only before { } | or,
// inside plural and selectordinal, #.
func Compile(pattern string) (*Message, error) {
	p := &parser{s: pattern}
	nodes, err := p.message(0, argKind)
	if err != nil {
		return nil, err
	}
	return &Message{nodes: nodes}, nil
}

type parser struct {
	s   string
	pos int
}

func (p *parser) errorf(format string, a ...any) error {
	return fmt.Errorf("icu: %s at offset %d", fmt.Sprintf(format, a...), p.pos)
}

func (p *parser) eof() bool { return p.pos >= len(p.s) }

func (p *parser) skipSpace() {
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.s[p.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		p.pos += size
	}
}

func (p *parser) consume(c byte) bool {
	if !p.eof() && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *parser) ident() string {
	start := p.pos
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.s[p.pos:])
		if unicode.IsSpace(r) || strings.ContainsRune("{}#,:'|=", r) {
			break
		}
		p.pos += size
	}
	return p.s[start:p.pos]
}

// message parses until EOF (depth 0) or an unconsumed closing brace.
func (p *parser) message(depth int, parent kind) ([]node, error) {
	var nodes []node
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			nodes = append(nodes, node{kind: textKind, text: text.String()})
			text.Reset()
		}
	}
	inPlural := parent == pluralKind || parent == ordinalKind
	for !p.eof() {
		switch c := p.s[p.pos]; {
		case c == '{':
			p.pos++
			flush()
			n, err := p.argument(depth)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, n)
		case c == '}' && depth > 0:
			flush()
			return nodes, nil
		case c == '#' && inPlural:
			p.pos++
			flush()
			nodes = append(nodes, node{kind: poundKind})
		case c == '\'':
			p.quoted(&text, inPlural)
		default:
			text.WriteByte(c)
			p.pos++
		}
	}
	if depth > 0 {
		return nil, p.errorf("unclosed brace")
	}
	flush()
	return nodes, nil
}

func (p *parser) quoted(text *strings.Builder, inPlural bool) {
	p.pos++
	if p.eof() {
		text.WriteByte('\'')
		return
	}
	switch next := p.s[p.pos]; {
	case next == '\'':
		p.pos++
		text.WriteByte('\'')
		return
	case next == '{' || next == '}' || next == '|' || (next == '#' && inPlural):
	default:
		text.WriteByte('\'')
		return
	}
	for !p.eof() {
		c := p.s[p.pos]
		p.pos++
		if c != '\'' {
			text.WriteByte(c)
			continue
		}
		if !p.consume('\'') {
			return
		}
		text.WriteByte('\'')
	}
}

func (p *parser) argument(depth int) (node, error) {
	p.skipSpace()
	name := p.ident()
	if name == "" {
		return node{}, p.errorf("expected argument name")
	}
	p.skipSpace()
	if p.consume('}') {
		return node{kind: argKind, text: name}, nil
	}
	if !p.consume(',') {
		return node{}, p.errorf("expected ',' or '}'")
	}
	p.skipSpace()
	typ := p.ident()
	p.skipSpace()
	switch typ {
	case "number":
		return p.numberArg(name)
	case "plural":
		return p.options(node{kind: pluralKind, text: name}, depth)
	case "selectordinal":
		return p.options(node{kind: ordinalKind, text: name}, depth)
	case "select":
		return p.options(node{kind: selectKind, text: name}, depth)
	case "":
		return node{}, p.errorf("expected argument type")
	default:
		return node{}, p.errorf("unsupported argument type %q", typ)
	}
}

func (p *parser) numberArg(name string) (node, error) {
	n := node{kind: numberKind, text: name}
	if p.consume(',') {
		p.skipSpace()
		switch s := p.ident(); s {
		case "integer":
			n.style = integerStyle
		case "percent":
			n.style = percentStyle
		default:
			return node{}, p.errorf("unsupported number style %q", s)
		}
		p.skipSpace()
	}
	if !p.consume('}') {
		return node{}, p.errorf("expected '}'")
	}
	return n, nil
}

func (p *parser) options(n node, depth int) (node, error) {
	if !p.consume(',') {
		return node{}, p.errorf("expected ',' after argument type")
	}
	n.cases = make(map[string][]node)
	p.skipSpace()
	if n.kind == pluralKind && strings.HasPrefix(p.s[p.pos:], "offset:") {
		p.pos += len("offset:")
		p.skipSpace()
		start := p.pos
		for !p.eof() && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
		}
		off, err := strconv.ParseInt(p.s[start:p.pos], 10, 64)
		if err != nil {
			return node{}, p.errorf("invalid offset")
		}
		n.offset = off
	}
	seen := make(map[string]bool)
	for {
		p.skipSpace()
		if p.eof() {
			return node{}, p.errorf("unclosed %s", n.text)
		}
		if p.consume('}') {
			break
		}
		key, exact := "", false
		if p.consume('=') {
			key, exact = "="+p.ident(), true
		} else {
			key = p.ident()
		}
		if key == "" || seen[key] {
			return node{}, p.errorf("invalid or duplicate selector %q", key)
		}
		seen[key] = true
		var value float64
		if exact {
			if n.kind == selectKind {
				return node{}, p.errorf("exact selector in select")
			}
			v, err := strconv.ParseFloat(key[1:], 64)
			if err != nil {
				return node{}, p.errorf("invalid selector %q", key)
			}
			value = v
		}
		p.skipSpace()
		if !p.consume('{') {
			return node{}, p.errorf("expected '{' after selector %q", key)
		}
		msg, err := p.message(depth+1, n.kind)
		if err != nil {
			return node{}, err
		}
		p.pos++
		if exact {
			n.exact = append(n.exact, exactCase{value: value, msg: msg})
		} else {
			n.cases[key] = msg
		}
	}
	if _, ok := n.cases["other"]; !ok {
		return node{}, p.errorf("missing 'other' selector")
	}
	return n, nil
}
