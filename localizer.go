package airstrings

import (
	"github.com/symbionix-sl/airstrings-sdk-go/internal/bundle"
	"github.com/symbionix-sl/airstrings-sdk-go/internal/icu"
)

// snapshot is one applied bundle revision, immutable once published.
type snapshot struct {
	locale   string
	revision int64
	icu      *icu.Locale
	entries  map[string]entry
}

type entry struct {
	value      string
	isICU      bool
	msg        *icu.Message // nil for text entries and patterns that do not compile
	sel        *bundle.Selector
	choiceMsgs []*icu.Message // compiled Choice values of an ICU entry, aligned with sel.Choices
}

func newSnapshot(b *bundle.Bundle, loc *icu.Locale) *snapshot {
	s := &snapshot{locale: b.Locale, revision: b.Revision, icu: loc, entries: make(map[string]entry, len(b.Strings))}
	for key, be := range b.Strings {
		e := entry{value: be.Value, isICU: be.Format == "icu", sel: bundle.NewSelector(be.Experiment, be.Value)}
		if e.isICU {
			e.msg = compile(be.Value)
			if e.sel != nil {
				e.choiceMsgs = make([]*icu.Message, len(e.sel.Choices))
				for i, c := range e.sel.Choices {
					e.choiceMsgs[i] = compile(c.Value)
				}
			}
		}
		s.entries[key] = e
	}
	return s
}

func compile(pattern string) *icu.Message {
	m, err := icu.Compile(pattern)
	if err != nil {
		return nil
	}
	return m
}

// Localizer reads strings for one locale and one assignment from a single
// bundle revision, so everything rendered with it is consistent. Create one
// per request; it is cheap and safe for concurrent use.
type Localizer struct {
	snap         *snapshot
	assignmentID string
	exposures    *exposures
}

// T returns the string for key, the selected experiment variant when the
// Localizer has an assignment, or key itself when the string is unknown.
// ICU strings are returned unformatted.
func (l *Localizer) T(key string) string {
	if l.snap == nil {
		return key
	}
	e, ok := l.snap.entries[key]
	if !ok {
		return key
	}
	value, _ := l.resolve(key, &e)
	return value
}

// Format returns the ICU string for key formatted with args. Text strings are
// returned as-is. If formatting fails (missing or mistyped argument, or an
// unsupported pattern) it returns the raw pattern; unknown keys return key.
func (l *Localizer) Format(key string, args map[string]any) string {
	if l.snap == nil {
		return key
	}
	e, ok := l.snap.entries[key]
	if !ok {
		return key
	}
	value, msg := l.resolve(key, &e)
	if msg == nil {
		return value
	}
	s, err := msg.Format(l.snap.icu, args)
	if err != nil {
		return value
	}
	return s
}

func (l *Localizer) resolve(key string, e *entry) (string, *icu.Message) {
	if e.sel == nil || l.assignmentID == "" {
		return e.value, e.msg
	}
	i := e.sel.Pick(l.assignmentID)
	c := &e.sel.Choices[i]
	if !c.OK {
		return e.value, e.msg
	}
	l.exposures.record(Exposure{Key: key, ExperimentID: e.sel.ID, Variant: c.Variant, Locale: l.snap.locale, AssignmentID: l.assignmentID})
	if c.Variant == "control" || !e.isICU {
		return c.Value, e.msg
	}
	return c.Value, e.choiceMsgs[i]
}
