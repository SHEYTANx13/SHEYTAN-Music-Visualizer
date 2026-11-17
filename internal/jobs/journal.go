// journal.go — bounded, crash-reconcilable job journal.
//
// Design (Section 2.6 of the orchestration contract):
//   - Only job METADATA is persisted. Live pointers, OS handles, device
//     objects, GPU resources and secret content are never serialized
//     (the schema below contains exclusively scalar fields chosen by
//     the scheduler).
//   - Appends are single complete JSON lines; the file is fsynced on
//     close and compacted atomically (tmp + rename) when it exceeds
//     the line budget, keeping the last 1024 records.
//   - On open, entries whose last state is still queued/scheduled/
//     running are marked interrupted: a crash can never leave a job
//     implicitly "successful", and orphaned running jobs surface as
//     interrupted, never as live.
package jobs

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	journalSchema    = 1
	journalMaxLines  = 4096
	journalKeepLines = 1024
)

// journalRecord is one persisted line. errText is the safe user-facing
// summary the job produced (already redacted upstream); full sensitive
// paths never appear here by construction — jobs carry opaque
// correlation IDs and fingerprints.
type journalRecord struct {
	Schema        int           `json:"schema"`
	ID            uint64        `json:"id"`
	Type          string        `json:"type"`
	Lane          Lane          `json:"lane"`
	Event         string        `json:"event"` // queued|running|retry|coalesced|cancelled|terminal|interrupted
	State         State         `json:"state"`
	Attempt       int           `json:"attempt"`
	Correlation   string        `json:"correlation,omitempty"`
	Stage         string        `json:"stage,omitempty"`
	ErrorCategory ErrorCategory `json:"errCat,omitempty"`
	At            time.Time     `json:"at"`
}

// journal is the bounded on-disk job journal.
type journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	w    *bufio.Writer
	n    int // lines written since open (including reconciled)
}

// openJournal opens (creating if needed) the journal and reconciles a
// previous crash: the last state of every job id is derived from the
// existing records; ids still in a live state get an "interrupted"
// record appended immediately so the file always tells the truth.
func openJournal(path string) (*journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	j := &journal{path: path}
	// Reconciliation pass (read-only on the old content).
	last := map[uint64]journalRecord{}
	if rf, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(rf)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var r journalRecord
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.Schema == journalSchema {
				last[r.ID] = r
			}
		}
		rf.Close()
		lines := 0
		for range last { // approximate; good enough for the budget
			lines++
		}
		j.n = lines
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	j.f = f
	j.w = bufio.NewWriter(f)
	for id, r := range last {
		switch r.State {
		case StateQueued, StateScheduled, StateRunning:
			// Orphaned live job from a crashed session: mark interrupted.
			ir := journalRecord{Schema: journalSchema, ID: id, Type: r.Type, Lane: r.Lane,
				Event: "interrupted", State: StateFailed, Attempt: r.Attempt,
				Correlation: r.Correlation, ErrorCategory: ErrorInterrupted, At: time.Now().UTC()}
			j.append(ir)
		}
	}
	return j, nil
}

// record appends one metadata line for j.
func (j *journal) record(jb *Job, event string) {
	if j == nil {
		return
	}
	jb.mu.RLock()
	r := journalRecord{Schema: journalSchema, ID: jb.ID, Type: jb.Type, Lane: jb.Lane,
		Event: event, State: jb.state, Attempt: jb.attempt,
		Correlation: jb.CorrelationID, Stage: jb.stage, ErrorCategory: jb.errCategory,
		At: time.Now().UTC()}
	jb.mu.RUnlock()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.append(r)
}

// append is the internal locked writer with compaction. Every record
// is flushed to the OS immediately: job transitions are low-rate
// events and a crash between records must never lose the last state
// (fsync stays reserved for close/compact — the OS page cache survives
// a process crash, which is the failure mode the journal reconciles).
func (j *journal) append(r journalRecord) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	j.w.Write(b)
	j.w.WriteByte('\n')
	j.w.Flush()
	j.n++
	if j.n >= journalMaxLines {
		j.compactLocked()
	}
}

// compactLocked rewrites the journal keeping the most recent records.
// The rewrite goes to a temp file and is promoted atomically, so a
// crash mid-compaction leaves either the old or the new complete file.
func (j *journal) compactLocked() {
	j.w.Flush()
	j.f.Sync()
	// Read all current lines.
	lines := make([][]byte, 0, journalMaxLines)
	if rf, err := os.Open(j.path); err == nil {
		sc := bufio.NewScanner(rf)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			l := make([]byte, len(sc.Bytes()))
			copy(l, sc.Bytes())
			lines = append(lines, l)
		}
		rf.Close()
	}
	if len(lines) > journalKeepLines {
		lines = lines[len(lines)-journalKeepLines:]
	}
	tmp := j.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return // keep appending to the old file; try again next time
	}
	tw := bufio.NewWriter(tf)
	for _, l := range lines {
		tw.Write(l)
		tw.WriteByte('\n')
	}
	tw.Flush()
	tf.Sync()
	tf.Close()
	j.f.Close()
	if err := os.Rename(tmp, j.path); err != nil {
		// Rename failed: reopen the original and continue un-compacted.
		j.f, _ = os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		j.w = bufio.NewWriter(j.f)
		return
	}
	j.f, err = os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	j.w = bufio.NewWriter(j.f)
	j.n = len(lines)
}

// close flushes and closes the journal.
func (j *journal) close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.w != nil {
		j.w.Flush()
	}
	if j.f != nil {
		j.f.Sync()
		j.f.Close()
	}
}
