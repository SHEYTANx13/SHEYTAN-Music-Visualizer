// undo.go — bounded, revision-scoped command undo/redo for library
// mutations (run-43767556517 §3.12 UNDO/REDO). Every mutating Library
// command pushes a reversible record with parameter-bound inverse
// closures; undo/redo restore exact fields, names, paths and revisions
// — never merely a UI selection.
//
// Safety rules:
//   - records are scoped to the entity ID; if the referenced entity can
//     no longer be located (purged), the record is refused with an
//     explanation instead of guessing;
//   - re-applied snapshots bump the revision (monotonic), so stale
//     editors keep receiving honest conflict errors instead of silent
//     clobbering;
//   - inverse moves are journalled like every other move transaction;
//   - the stack is bounded (maxUndoRecords, oldest dropped first) and
//     is process-local: it is never persisted, because undoing across
//     app restarts cannot prove the world did not change meanwhile.

package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/project"
)

// maxUndoRecords bounds the undo/redo history (memory, not disk).
const maxUndoRecords = 50

type opKind uint8

const (
	opCreate  opKind = iota // create/import (undo = trash, redo = restore)
	opUpdate                // edit (undo/redo = exact document snapshots)
	opRename                // rename/move (undo/redo = exact path move)
	opTrash                 // soft delete (undo = restore, redo = trash)
	opRestore               // restore (undo = trash, redo = restore)
)

type entry struct {
	kind  opKind
	id    string
	label string

	// undo applies the inverse of the original command; redo re-applies
	// it. Both are service-internal operations bound to the exact ids,
	// paths and snapshots of the recorded command.
	undo func() error
	redo func() error

	prevRev  uint64 // revision before the original command
	before   []byte // update: serialized document before the edit
	after    []byte // update: serialized document after the edit
	fromPath string // rename/trash/restore: original path
	toPath   string // rename/trash/restore: resulting path
}

// undoStack is a classic two-stack undo/redo buffer.
type undoStack struct {
	undo []entry
	redo []entry
}

func (s *undoStack) push(e entry) {
	s.undo = append(s.undo, e)
	if len(s.undo) > maxUndoRecords {
		s.undo = s.undo[len(s.undo)-maxUndoRecords:]
	}
	s.redo = nil // a new command invalidates the redo branch
}

// pushUndo records a completed command (lock held).
func (l *Library) pushUndo(e entry) { l.undo.push(e) }

// locate returns the observed record for id (live or trashed).
func (l *Library) locate(id string) (*record, error) {
	if r, ok := l.rec[id]; ok {
		return r, nil
	}
	if p := l.findTrashedLocked(id); p != "" {
		return &record{path: p}, nil
	}
	return nil, ErrUnknownEntity
}

// canApply refuses a record whose entity can no longer be located at
// all (purged): undoing it would silently guess. Everything narrower
// (a later rename moved the file) is verified by the inverse operation
// itself, which fails precisely without corrupting anything.
func (l *Library) canApply(e entry) error {
	if _, err := l.locate(e.id); err != nil {
		return fmt.Errorf("%w: entity %s no longer exists (it was purged or removed outside the library)", ErrUndoUnavailable, e.id)
	}
	return nil
}

// recordFromBytes rebuilds the observed record from document bytes.
func recordFromBytes(path string, raw []byte) (*record, error) {
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("content: document unparsable: %w", err)
	}
	return &record{path: path, hash: bytesHash(raw), rev: p.Revision}, nil
}

// internalWrite persists exact serialized document bytes with a bumped
// revision, updating the observed record. Used only by undo/redo so a
// re-applied snapshot never shadows an external edit silently.
func (l *Library) internalWrite(path string, docBytes []byte) error {
	var p project.Project
	if err := json.Unmarshal(docBytes, &p); err != nil {
		return fmt.Errorf("content: undo snapshot unparsable: %w", err)
	}
	p.Revision++ // monotonic: undo/redo never rewrites history invisibly
	if err := project.Save(&p, path); err != nil {
		return fmt.Errorf("content: undo write: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("content: undo verify: %w", err)
	}
	rec, err := recordFromBytes(path, raw)
	if err != nil {
		return err
	}
	l.rec[p.ContentID] = rec
	return nil
}

// moveForUndo performs a journalled move between two exact paths and
// refreshes the observed record. It refuses instead of guessing when
// the source is not where the record expects it.
func (l *Library) moveForUndo(id, from, to string) error {
	if _, err := os.Lstat(from); err != nil {
		return fmt.Errorf("%w: expected %s at %s", ErrUndoUnavailable, id, filepath.Base(from))
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%w: destination %s already exists", ErrUndoUnavailable, filepath.Base(to))
	}
	journal, rollback, jerr := writeJournal(l.root, "undo-move", id, from, to, 0, 0)
	if jerr != nil {
		return jerr
	}
	if err := os.Rename(from, to); err != nil {
		_ = rollback()
		return fmt.Errorf("content: undo move: %w", err)
	}
	_ = os.Remove(tombstonePath(from))
	if err := os.Remove(journal); err != nil {
		return fmt.Errorf("content: journal removal: %w", err)
	}
	raw, err := os.ReadFile(to)
	if err != nil {
		return fmt.Errorf("content: undo move verify: %w", err)
	}
	rec, err := recordFromBytes(to, raw)
	if err != nil {
		return err
	}
	l.rec[id] = rec
	delete(l.trashRec, id)
	return nil
}

// trashMoveForUndo moves a live entity into the Trash area for a known
// id, bypassing the public revision preconditions (the undo record
// carries the exact recorded paths).
func (l *Library) trashMoveForUndo(id, reason string) error {
	r, err := l.locate(id)
	if err != nil {
		return err
	}
	from := r.path
	if filepath.Dir(from) != l.root {
		return fmt.Errorf("%w: %s is not a live library document", ErrUndoUnavailable, id)
	}
	name := filepath.Base(from)
	to := filepath.Join(l.trash, name)
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%w: trash already contains %s", ErrUndoUnavailable, name)
	}
	journal, rollback, jerr := writeJournal(l.root, "undo-trash", id, from, to, 0, 0)
	if jerr != nil {
		return jerr
	}
	if err := os.Rename(from, to); err != nil {
		_ = rollback()
		return fmt.Errorf("content: undo trash move: %w", err)
	}
	if terr := l.writeTombstoneLocked(to, id, reason); terr != nil {
		_ = os.Rename(to, from)
		_ = rollback()
		return terr
	}
	if err := os.Remove(journal); err != nil {
		return fmt.Errorf("content: journal removal: %w", err)
	}
	delete(l.rec, id)
	l.trashRec[id] = to
	return nil
}

// restoreMoveForUndo returns a trashed document to the live library for
// a known id (identity and revision history preserved).
func (l *Library) restoreMoveForUndo(id string) error {
	from := l.findTrashedLocked(id)
	if from == "" {
		return fmt.Errorf("%w: %s is not in the Trash area", ErrUndoUnavailable, id)
	}
	name := filepath.Base(from)
	to := filepath.Join(l.root, name)
	if _, err := os.Lstat(to); err == nil {
		base := strings.TrimSuffix(name, projectExt) + " (restored)"
		safe, serr := safeFileName(l.root, base, projectExt)
		if serr != nil {
			return serr
		}
		to = filepath.Join(l.root, safe)
	}
	return l.moveForUndo(id, from, to)
}

// Undo reverts the most recent command when the world still matches
// what the record expects. Returns the human-readable label of the
// reverted command (for status display).
func (l *Library) Undo() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.undo.undo) == 0 {
		return "", ErrUndoUnavailable
	}
	e := l.undo.undo[len(l.undo.undo)-1]
	if err := l.canApply(e); err != nil {
		// Permanently unapplicable (purged entity): drop the record with
		// the explanation; the stack stays consistent and the caller
		// keeps its draft.
		l.undo.undo = l.undo.undo[:len(l.undo.undo)-1]
		return "", err
	}
	if err := e.undo(); err != nil {
		return "", err
	}
	l.undo.undo = l.undo.undo[:len(l.undo.undo)-1]
	l.undo.redo = append(l.undo.redo, e)
	return e.label, nil
}

// Redo re-applies the most recently undone command under the same
// exact-state rules.
func (l *Library) Redo() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.undo.redo) == 0 {
		return "", ErrUndoUnavailable
	}
	e := l.undo.redo[len(l.undo.redo)-1]
	if err := l.canApply(e); err != nil {
		l.undo.redo = l.undo.redo[:len(l.undo.redo)-1]
		return "", err
	}
	if err := e.redo(); err != nil {
		return "", err
	}
	l.undo.redo = l.undo.redo[:len(l.undo.redo)-1]
	l.undo.undo = append(l.undo.undo, e)
	return e.label, nil
}

// UndoDepth reports the undo/redo stack depths (UI affordances).
func (l *Library) UndoDepth() (undo, redo int) {
	return len(l.undo.undo), len(l.undo.redo)
}
