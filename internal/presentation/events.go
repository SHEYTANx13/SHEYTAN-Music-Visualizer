package presentation

import (
	"sync"
	"time"
)

// EventKind is a stable event vocabulary shared by both shells. Names
// mirror the core event families (transport, export, renderer, UI
// state) so shell adapters never invent private strings.
type EventKind string

// Canonical event kinds. Append-only; never rename.
const (
	EvTransportState   EventKind = "transport.state"
	EvTransportClock   EventKind = "transport.clock"
	EvLibraryChanged   EventKind = "library.changed"
	EvExportProgress   EventKind = "export.progress"
	EvExportDone       EventKind = "export.done"
	EvRendererChanged  EventKind = "renderer.changed"
	EvThemeChanged     EventKind = "theme.changed"
	EvDestinationFocus EventKind = "navigation.focus"
	EvDiagnostics      EventKind = "diagnostics.snapshot"
	EvMediaToolsState  EventKind = "mediatools.state"
	EvErrorRaised      EventKind = "error.raised"
)

// Envelope is the typed event envelope. Payload is a versioned DTO
// value (never a mutable domain struct, never a GPU frame).
type Envelope struct {
	Kind       EventKind   `json:"kind"`
	Generation uint64      `json:"generation"` // stale-frame/session guard
	At         time.Time   `json:"at"`
	Seq        uint64      `json:"seq"` // per-bus monotonic sequence
	Payload    interface{} `json:"payload"`
}

// EventBus delivers envelopes to shell adapters with bounded memory:
// high-frequency sources (clock ticks, render progress) use
// coalescing latest-value delivery so a slow shell can never accumulate
// an unbounded backlog; discrete sources deliver every event with a
// small bounded queue and drop-oldest overflow accounting.
type EventBus struct {
	mu         sync.Mutex
	seq        uint64
	generation uint64
	latest     map[EventKind]Envelope
	discrete   []Envelope
	dropped    uint64
	capacity   int
}

// NewEventBus creates a bus with the given discrete queue capacity
// (minimum 16). Coalesced kinds (EvTransportClock, EvExportProgress)
// keep only the most recent envelope per kind.
func NewEventBus(capacity int) *EventBus {
	if capacity < 16 {
		capacity = 16
	}
	return &EventBus{
		latest:   make(map[EventKind]Envelope),
		capacity: capacity,
	}
}

// Publish records one envelope. seq is assigned monotonically.
// Generation guards stale delivery across playback sessions and scene
// reloads: a shell ignores envelopes whose generation predates the
// session it renders.
func (b *EventBus) Publish(kind EventKind, generation uint64, payload interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	env := Envelope{Kind: kind, Generation: generation, At: time.Now().UTC(), Seq: b.seq, Payload: payload}
	switch kind {
	case EvTransportClock, EvExportProgress:
		b.latest[kind] = env // coalesced: keep only the newest
	default:
		if len(b.discrete) >= b.capacity {
			copy(b.discrete, b.discrete[1:])
			b.discrete[len(b.discrete)-1] = env
			b.dropped++
			return
		}
		b.discrete = append(b.discrete, env)
	}
}

// Drain removes and returns all pending envelopes (coalesced latest
// values first, then discrete in order). Draining also resets dropped
// accounting into the report.
func (b *EventBus) Drain() ([]Envelope, Dropped) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Envelope, 0, len(b.latest)+len(b.discrete))
	for _, kind := range []EventKind{EvTransportClock, EvExportProgress} {
		if env, ok := b.latest[kind]; ok {
			out = append(out, env)
			delete(b.latest, kind)
		}
	}
	out = append(out, b.discrete...)
	b.discrete = nil
	return out, Dropped{Count: b.dropped}
}

// Dropped reports overflow accounting since the last drain.
type Dropped struct {
	Count uint64
}

// SetGeneration updates the bus generation (session guard) and clears
// pending state: pending envelopes from an older session must never
// reach a shell rendering the new one.
func (b *EventBus) SetGeneration(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.generation = generation
	b.latest = make(map[EventKind]Envelope)
	b.discrete = nil
}
