package bundle

import (
	"crypto/sha256"
	"encoding/binary"
	"maps"
	"slices"
)

// Bucket is BE_uint32(SHA-256(experimentID + ":" + assignmentID)[0:4]) mod 100.
func Bucket(experimentID, assignmentID string) int {
	var buf [128]byte
	in := append(buf[:0], experimentID...)
	in = append(in, ':')
	in = append(in, assignmentID...)
	sum := sha256.Sum256(in)
	return int(binary.BigEndian.Uint32(sum[:4]) % 100)
}

// Choice is one allocation slot. OK is false when a non-control variant has no
// value for this locale, which makes the experiment invalid for that bucket.
type Choice struct {
	Variant string
	Value   string
	OK      bool
}

// Selector resolves an assignment to a Choice with a precomputed bucket table.
type Selector struct {
	ID      string
	Choices []Choice
	table   [100]uint8
}

// NewSelector returns nil when the experiment is invalid: empty id, or weights
// that are negative or do not sum to exactly 100.
func NewSelector(e *Experiment, base string) *Selector {
	if e == nil || e.ID == "" {
		return nil
	}
	var sum int64
	for _, w := range e.Allocation {
		if w < 0 || w > 100 {
			return nil
		}
		sum += w
	}
	if sum != 100 {
		return nil
	}
	s := &Selector{ID: e.ID}
	acc := 0
	for _, name := range slices.Sorted(maps.Keys(e.Allocation)) {
		w := int(e.Allocation[name])
		if w == 0 {
			continue
		}
		c := Choice{Variant: name, Value: base, OK: true}
		if name != "control" {
			c.Value, c.OK = e.Variants[name]
		}
		for i := acc; i < acc+w; i++ {
			s.table[i] = uint8(len(s.Choices))
		}
		s.Choices = append(s.Choices, c)
		acc += w
	}
	return s
}

// Pick returns the index into Choices for assignmentID.
func (s *Selector) Pick(assignmentID string) int {
	return int(s.table[Bucket(s.ID, assignmentID)])
}
