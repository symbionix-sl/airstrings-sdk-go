package airstrings

import (
	"sync"
	"sync/atomic"
)

// Exposure records that an experiment variant, or "control", was served to an
// assignment. Join it to conversions on AssignmentID.
type Exposure struct {
	Key          string
	ExperimentID string
	Variant      string
	Locale       string
	AssignmentID string
}

// ponytail: the dedupe set is replaced once it exceeds 100_000 entries, so an
// exposure can be delivered again after a reset; swap in an LRU if exact
// once-per-process delivery matters.
const maxExposures = 100_000

type exposureKey struct{ key, experimentID, variant, assignmentID string }

// exposures delivers each distinct exposure once. Locale is deliberately not
// part of the dedupe key. Repeat reads hit the lock-free path of sync.Map.
type exposures struct {
	handler func(Exposure)
	limit   int64
	count   atomic.Int64
	seen    atomic.Pointer[sync.Map]
}

func newExposures(handler func(Exposure)) *exposures {
	x := &exposures{handler: handler, limit: maxExposures}
	x.seen.Store(new(sync.Map))
	return x
}

func (x *exposures) record(e Exposure) {
	if x.handler == nil {
		return
	}
	k := exposureKey{e.Key, e.ExperimentID, e.Variant, e.AssignmentID}
	seen := x.seen.Load()
	if _, ok := seen.Load(k); ok {
		return
	}
	if _, loaded := seen.LoadOrStore(k, struct{}{}); loaded {
		return
	}
	if x.count.Add(1) > x.limit && x.seen.CompareAndSwap(seen, new(sync.Map)) {
		x.count.Store(0)
	}
	go x.handler(e)
}
