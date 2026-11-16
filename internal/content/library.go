// library.go — the Library: the validated single mutation boundary for
// every persisted project document inside the managed projects root
// (run-43767556517 §3.12). See package documentation for the contract.
//
// Managed layout (the existing project file format and extension are
// preserved byte-for-byte on disk; only additive schema fields are
// introduced):
//
//      <root>/My Song.sheytan.json          live project documents
//      <root>/.trash/<name>.sheytan.json    soft-deleted documents (ID intact)
//      <root>/.trash/<name>.sheytan.json.meta.json   tombstone metadata
//      <root>/.journal-<op>.json            in-flight move transaction
//      <root>/.sheytan-project-*.tmp        project.Save staging (crash leftovers
//                                           are cleaned by Open)
//
// The library NEVER writes outside <root> and NEVER deletes anything
// outside <root>; the external source media referenced by MediaRef is
// outside its ownership by definition.

package content

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/project"
)

// Library is the single writer for the managed projects root. All
// commands are serialized by an internal mutex; each is a logical
// transaction with the semantics documented per method.
type Library struct {
	root     string
	trash    string
	mu       sync.Mutex
	rec      map[string]*record // live id -> observed state (path, hash, chash)
	trashRec map[string]string  // trashed id -> document path
	undo     undoStack
	// RenderGuard, when set by the host, returns true when the entity
	// with this ID is still consumed by an active render/export job;
	// destructive commands then fail with ErrRenderActive instead of
	// racing a renderer. Projects render from immutable snapshots, so
	// this is a courtesy gate for live-preview content, not a data
	// protection mechanism (the snapshot itself is already immutable).
	RenderGuard func(id string) bool
}

type record struct {
	path  string
	hash  string // sha256 of last-observed document bytes
	chash string // sha256 of the canonical (id/revision-stripped) document
	rev   uint64
}

// CreateRequest carries everything needed to persist a new entity.
type CreateRequest struct {
	Name        string           // display name (collision-safe file names)
	SourcePath  string           // external media location (NOT copied/owned)
	Fingerprint string           // media content fingerprint
	Container   string           // media container
	Duration    float64          // media duration in seconds
	Settings    *project.Project // optional full document; when nil a validated default is created
}

// Open initializes (and recovers) the library at root. Recovery is
// deterministic: every journal left behind by an interrupted
// rename-class transaction is finished or rolled back based on what is
// actually on disk, and stale project.Save staging files are removed.
// The number of recovered transactions is returned for diagnostics.
func Open(root string) (*Library, error) {
	l := &Library{
		root:     root,
		trash:    filepath.Join(root, ".trash"),
		rec:      map[string]*record{},
		trashRec: map[string]string{},
	}
	if err := os.MkdirAll(l.trash, 0o755); err != nil {
		return nil, fmt.Errorf("content: trash area: %w", err)
	}
	if _, err := recoverJournals(root); err != nil {
		return nil, err
	}
	if _, err := cleanStaleStaging(root); err != nil {
		return nil, err
	}
	if err := l.rescanLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

// Root returns the managed directory (diagnostics display only).
func (l *Library) Root() string { return l.root }

// recoverJournals replays every left-behind journal deterministically.
// For a rename journal the possible end states are: (a) destination
// exists -> transaction committed, drop the journal; (b) only the
// source exists -> transaction never ran, drop the journal (rollback
// is a no-op); (c) neither exists -> the transaction is undone by
// definition, drop the journal. Both-present is impossible under
// atomic rename and is treated as recovery-required: the destination
// wins, the source copy is preserved as "name (recovered).sheytan.json"
// so no bytes are ever destroyed during recovery.
func recoverJournals(root string) (int, error) {
	entries, err := filepath.Glob(filepath.Join(root, ".journal-*.json"))
	if err != nil {
		return 0, fmt.Errorf("content: journal scan: %w", err)
	}
	recovered := 0
	for _, j := range entries {
		raw, err := os.ReadFile(j)
		if err != nil {
			return recovered, fmt.Errorf("content: journal %s unreadable: %w", filepath.Base(j), err)
		}
		var jr journalRec
		if err := json.Unmarshal(raw, &jr); err != nil || jr.From == "" || jr.To == "" {
			// A journal we cannot parse cannot be finished safely; keep
			// it on disk and surface it (fail-safe, never silently drop).
			return recovered, fmt.Errorf("content: journal %s is corrupt — manual inspection required (no files were changed)", filepath.Base(j))
		}
		_, fromErr := os.Stat(jr.From)
		_, toErr := os.Stat(jr.To)
		switch {
		case toErr == nil && fromErr == nil:
			// Both exist: keep both — preserve the source under a
			// recovery name, then drop the journal.
			alt, ferr := safeFileName(filepath.Dir(jr.From), strings.TrimSuffix(filepath.Base(jr.From), projectExt)+" (recovered)", projectExt)
			if ferr != nil {
				return recovered, fmt.Errorf("content: recovery name for %s: %w", jr.From, ferr)
			}
			if err := os.Rename(jr.From, filepath.Join(filepath.Dir(jr.From), alt)); err != nil {
				return recovered, fmt.Errorf("content: recovery move for %s: %w", jr.From, err)
			}
		case toErr == nil:
			// Committed before the crash: the destination is authoritative.
		case fromErr == nil:
			// Never executed: source stays authoritative (rollback = no-op).
		default:
			// Fully undone by the crash itself.
		}
		if err := os.Remove(j); err != nil {
			return recovered, fmt.Errorf("content: journal removal: %w", err)
		}
		recovered++
	}
	return recovered, nil
}

// cleanStaleStaging removes leftover project.Save temp files (a crash
// between CreateTemp and Rename leaves exactly one; it is never
// referenced by anything and can hold no unique data the owner needs).
func cleanStaleStaging(root string) (int, error) {
	entries, err := filepath.Glob(filepath.Join(root, ".sheytan-project-*.tmp"))
	if err != nil {
		return 0, fmt.Errorf("content: staging scan: %w", err)
	}
	removed := 0
	for _, p := range entries {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("content: staging cleanup %s: %w", filepath.Base(p), err)
		}
		removed++
	}
	return removed, nil
}

// projectExt is the preserved project document extension.
const projectExt = ".sheytan.json"

// journalRec is the crash-recovery record for move-class transactions.
type journalRec struct {
	Op         string `json:"op"`     // rename
	EntityID   string `json:"id"`     // stable entity id
	From       string `json:"from"`   // absolute source path
	To         string `json:"to"`     // absolute destination path
	OldRev     uint64 `json:"oldRev"` // revision before the transaction
	NewRev     uint64 `json:"newRev"` // revision after the transaction
	StartedUTC string `json:"startedUtc"`
}

// writeJournal durably records a move transaction BEFORE it runs.
func writeJournal(root, op, id, from, to string, oldRev, newRev uint64) (string, func() error, error) {
	jr := journalRec{Op: op, EntityID: id, From: from, To: to, OldRev: oldRev, NewRev: newRev,
		StartedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, err := json.MarshalIndent(jr, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("content: marshal journal: %w", err)
	}
	journal := filepath.Join(root, fmt.Sprintf(".journal-%s-%d.json", op, time.Now().UnixNano()))
	f, err := os.OpenFile(journal, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return "", nil, fmt.Errorf("content: create journal: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(journal)
		return "", nil, fmt.Errorf("content: write journal: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(journal)
		return "", nil, fmt.Errorf("content: sync journal: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(journal)
		return "", nil, fmt.Errorf("content: close journal: %w", err)
	}
	rollback := func() error { return os.Remove(journal) }
	return journal, rollback, nil
}

// rescanLocked rebuilds the observed-state map from the directory (the
// directory is the source of truth; there is no persistent index).
func (l *Library) rescanLocked() error {
	rec := map[string]*record{}
	trashRec := map[string]string{}
	docs, err := filepath.Glob(filepath.Join(l.root, "*"+projectExt))
	if err != nil {
		return fmt.Errorf("content: library scan: %w", err)
	}
	trash, err := filepath.Glob(filepath.Join(l.trash, "*"+projectExt))
	if err != nil {
		return fmt.Errorf("content: trash scan: %w", err)
	}
	for _, path := range docs {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("content: read %s: %w", filepath.Base(path), err)
		}
		var p project.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			// A file we cannot parse is not a library entry this service
			// can own; surface a precise error instead of skipping (the
			// operator decides; never a silent partial library).
			return fmt.Errorf("content: document %s is not a parseable project: %w", filepath.Base(path), err)
		}
		if p.ContentID == "" {
			// Legacy document discovered in the managed root: assign and
			// persist identity now (migration-on-write; bytes otherwise
			// preserved). This is the only automatic write Open performs.
			id, iderr := NewID()
			if iderr != nil {
				return iderr
			}
			p.ContentID = id
			p.Revision = 1
			if err := project.Save(&p, path); err != nil {
				return fmt.Errorf("content: legacy id assignment for %s: %w", filepath.Base(path), err)
			}
			raw, err = os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("content: re-read %s: %w", filepath.Base(path), err)
			}
		}
		if _, exists := rec[p.ContentID]; exists {
			return fmt.Errorf("content: duplicate id %s (%s and %s) — run diagnostics, manual inspection required", p.ContentID, rec[p.ContentID].path, path)
		}
		chash, cerr := canonicalHash(&p)
		if cerr != nil {
			return cerr
		}
		rec[p.ContentID] = &record{path: path, hash: bytesHash(raw), chash: chash, rev: p.Revision}
	}
	for _, path := range trash {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("content: read trash %s: %w", filepath.Base(path), err)
		}
		var p project.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("content: trash document %s is not parseable: %w", filepath.Base(path), err)
		}
		if _, exists := trashRec[p.ContentID]; exists {
			return fmt.Errorf("content: duplicate trashed id %s — run diagnostics, manual inspection required", p.ContentID)
		}
		trashRec[p.ContentID] = path
	}
	l.rec = rec
	l.trashRec = trashRec
	return nil
}

// observe reads one document and updates the observed-state record.
func (l *Library) observe(id string) (*project.Project, *record, error) {
	r, ok := l.rec[id]
	if !ok {
		return nil, nil, ErrUnknownEntity
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			// The file vanished outside the library: resync from the
			// directory (the source of truth) and report honestly.
			if serr := l.rescanLocked(); serr != nil {
				return nil, nil, serr
			}
			return nil, nil, ErrUnknownEntity
		}
		return nil, nil, fmt.Errorf("content: read %s: %w", filepath.Base(r.path), err)
	}
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", project.ErrCorrupt, err)
	}
	if p.ContentID != id {
		return nil, nil, fmt.Errorf("content: document %s carries id %s, expected %s", filepath.Base(r.path), p.ContentID, id)
	}
	chash, cerr := canonicalHash(&p)
	if cerr != nil {
		return nil, nil, cerr
	}
	l.rec[id] = &record{path: r.path, hash: bytesHash(raw), chash: chash, rev: p.Revision}
	r = l.rec[id]
	return &p, r, nil
}

// checkPreconditions enforces the shared mutation preconditions, in
// order: the entity exists live; the document bytes still match the
// last OBSERVED hash (external-modification detection runs BEFORE any
// record refresh — an outside writer must never be silently adopted);
// the expected revision matches; then the record is refreshed.
func (l *Library) checkPreconditions(id string, expectedRev uint64) (*project.Project, *record, error) {
	r, ok := l.rec[id]
	if !ok {
		if l.findTrashedLocked(id) != "" {
			return nil, nil, ErrEntityTrashed
		}
		return nil, nil, ErrUnknownEntity
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			if serr := l.rescanLocked(); serr != nil {
				return nil, nil, serr
			}
			return nil, nil, ErrUnknownEntity
		}
		return nil, nil, fmt.Errorf("content: read %s: %w", filepath.Base(r.path), err)
	}
	if bytesHash(raw) != r.hash {
		return nil, nil, ErrExternalModification
	}
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", project.ErrCorrupt, err)
	}
	if p.ContentID != id {
		return nil, nil, fmt.Errorf("content: document %s carries id %s, expected %s", filepath.Base(r.path), p.ContentID, id)
	}
	if expectedRev != r.rev {
		return nil, nil, fmt.Errorf("%w: expected %d, persisted %d", ErrRevisionConflict, expectedRev, r.rev)
	}
	chash, cerr := canonicalHash(&p)
	if cerr != nil {
		return nil, nil, cerr
	}
	l.rec[id] = &record{path: r.path, hash: bytesHash(raw), chash: chash, rev: p.Revision}
	r = l.rec[id]
	return &p, r, nil
}

// renderBlocked consults the optional host render guard.
func (l *Library) renderBlocked(id string) bool {
	return l.RenderGuard != nil && l.RenderGuard(id)
}

// Create persists a new entity as one durable transaction. The
// document is validated BEFORE promotion (project.Save refuses invalid
// documents); a failure leaves no partial state and no visible entity.
func (l *Library) Create(req CreateRequest) (Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if req.Settings != nil {
		if req.Name != "" {
			req.Settings.Name = req.Name
		}
	} else {
		req.Settings = project.NewDefault(req.Name, req.SourcePath, req.Fingerprint, req.Container, req.Duration)
	}
	id, err := NewID()
	if err != nil {
		return Entity{}, err
	}
	doc := *req.Settings // detached copy: the caller keeps its draft
	doc.ContentID = id
	doc.Revision = 1
	name, err := safeFileName(l.root, req.Name, projectExt)
	if err != nil {
		return Entity{}, err
	}
	doc.Name = strings.TrimSuffix(name, projectExt)
	path := filepath.Join(l.root, name)
	if err := project.Save(&doc, path); err != nil {
		return Entity{}, fmt.Errorf("content: create %q: %w", req.Name, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Entity{}, fmt.Errorf("content: verify create: %w", err)
	}
	chash, cerr := canonicalHash(&doc)
	if cerr != nil {
		return Entity{}, cerr
	}
	l.rec[id] = &record{path: path, hash: bytesHash(raw), chash: chash, rev: doc.Revision}
	e := l.entityLocked(&doc, path, false, time.Time{}, "")
	l.pushUndo(entry{
		kind: opCreate, id: id, label: "Create " + doc.Name, prevRev: 0,
		undo: func() error { return l.trashMoveForUndo(id, "undo of create") },
		redo: func() error { return l.restoreMoveForUndo(id) },
	})
	return e, nil
}

// entityLocked builds the immutable snapshot from the live document.
func (l *Library) entityLocked(p *project.Project, path string, trashed bool, deletedAt time.Time, reason string) Entity {
	fi, _ := os.Stat(path)
	var modified time.Time
	if fi != nil {
		modified = fi.ModTime()
	}
	return Entity{
		ID: p.ContentID, Revision: p.Revision, Name: p.Name, Path: path, Trashed: trashed,
		DeletedAt: deletedAt, DeleteReason: reason,
		CreatedAt: modified, ModifiedAt: modified,
		MediaFileName: p.MediaRef.FileName, Fingerprint: p.MediaRef.Fingerprint, Duration: p.MediaRef.Duration,
	}
}

// Get returns the live entity snapshot for a stable ID.
func (l *Library) Get(id string) (Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, r, err := l.observe(id)
	if err != nil {
		if errors.Is(err, ErrUnknownEntity) && l.findTrashedLocked(id) != "" {
			return Entity{}, ErrEntityTrashed
		}
		return Entity{}, err
	}
	return l.entityLocked(p, r.path, false, time.Time{}, ""), nil
}

// List returns all live entities sorted deterministically (Name, then
// ID — never map order). Sorting never changes identity or
// relationships; it is a pure projection.
func (l *Library) List() ([]Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]string, 0, len(l.rec))
	for id := range l.rec {
		ids = append(ids, id)
	}
	out := make([]Entity, 0, len(ids))
	for _, id := range ids {
		p, r, err := l.observe(id)
		if err != nil {
			return nil, err
		}
		out = append(out, l.entityLocked(p, r.path, false, time.Time{}, ""))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ListTrashed returns the Trash-area entities (for a Trash/restore UI).
func (l *Library) ListTrashed() ([]Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	docs, err := filepath.Glob(filepath.Join(l.trash, "*"+projectExt))
	if err != nil {
		return nil, fmt.Errorf("content: trash scan: %w", err)
	}
	out := make([]Entity, 0, len(docs))
	for _, path := range docs {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("content: read trash %s: %w", filepath.Base(path), err)
		}
		var p project.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("content: trash document %s is not parseable: %w", filepath.Base(path), err)
		}
		meta := l.readTombstoneLocked(path)
		out = append(out, l.entityLocked(&p, path, true, meta.deletedAt, meta.Reason))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DeletedAt.Equal(out[j].DeletedAt) {
			return out[i].DeletedAt.After(out[j].DeletedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Update applies mut to the CURRENT persisted document as one
// transaction. Semantics (§3.12 EDIT/UPDATE):
//   - expectedRev must equal the persisted revision, else
//     ErrRevisionConflict (stale edits never overwrite newer ones);
//   - document bytes must match the last observed hash, else
//     ErrExternalModification;
//   - mut runs against the full document and the result is validated
//     in full (project.Save refuses invalid states) — no half edits;
//   - the stable ID is preserved; the revision is bumped by exactly one;
//   - a failure leaves the last committed state untouched.
func (l *Library) Update(id string, expectedRev uint64, mut func(*project.Project) error) (Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, r, err := l.checkPreconditions(id, expectedRev)
	if err != nil {
		return Entity{}, err
	}
	beforeRaw, rerr := os.ReadFile(r.path)
	if rerr != nil {
		return Entity{}, fmt.Errorf("content: snapshot before edit: %w", rerr)
	}
	if err := mut(p); err != nil {
		return Entity{}, fmt.Errorf("content: change rejected: %w", err)
	}
	if p.ContentID != id {
		return Entity{}, fmt.Errorf("content: mutation attempted to change the stable id (refused)")
	}
	if p.Revision != r.rev {
		return Entity{}, fmt.Errorf("content: mutation attempted to move the revision token itself (refused)")
	}
	p.Revision = r.rev + 1
	if err := project.Save(p, r.path); err != nil {
		return Entity{}, fmt.Errorf("content: update %s: %w", id, err)
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return Entity{}, fmt.Errorf("content: verify update: %w", err)
	}
	chash, cerr := canonicalHash(p)
	if cerr != nil {
		return Entity{}, cerr
	}
	l.rec[id] = &record{path: r.path, hash: bytesHash(raw), chash: chash, rev: p.Revision}
	l.pushUndo(entry{
		kind: opUpdate, id: id, label: "Edit " + p.Name, prevRev: r.rev,
		before: beforeRaw, after: raw,
		undo: func() error { return l.internalWrite(r.path, beforeRaw) },
		redo: func() error { return l.internalWrite(r.path, raw) },
	})
	return l.entityLocked(p, r.path, false, time.Time{}, ""), nil
}

// Rename changes the display AND managed file name without changing
// identity. Semantics (§3.12 RENAME/MOVE): the move is journalled and
// atomic; the document's Name field is then committed as a normal
// revision-bumping edit; any failure restores the previous name.
// A crash between the move and the name edit leaves a valid document
// under the new file name with the previous display name — every
// observable piece stays complete and recovery is deterministic.
func (l *Library) Rename(id string, expectedRev uint64, newName string) (Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, r, err := l.checkPreconditions(id, expectedRev)
	if err != nil {
		return Entity{}, err
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return Entity{}, ErrInvalidName
	}
	if _, err := safeFileName(l.root, newName, projectExt); err != nil {
		return Entity{}, err
	}
	target := filepath.Join(l.root, strings.TrimSuffix(newName, projectExt)+projectExt)
	from, to := r.path, target
	beforeRaw, rerr := os.ReadFile(from)
	if rerr != nil {
		return Entity{}, fmt.Errorf("content: rename snapshot: %w", rerr)
	}

	if target != r.path {
		if _, err := os.Lstat(target); err == nil {
			return Entity{}, fmt.Errorf("%w: %s", ErrNameTaken, filepath.Base(target))
		}
		journal, rollback, jerr := writeJournal(l.root, "rename", id, from, to, p.Revision, p.Revision)
		if jerr != nil {
			return Entity{}, jerr
		}
		if err := os.Rename(from, to); err != nil {
			_ = rollback()
			return Entity{}, fmt.Errorf("content: rename: %w", err)
		}
		if err := os.Remove(journal); err != nil {
			return Entity{}, fmt.Errorf("content: journal removal: %w", err)
		}
		l.rec[id] = &record{path: to, hash: r.hash, rev: r.rev}
	}

	// Commit the display-name edit at the new path (single atomic
	// write; invalid states are refused by project.Save).
	p.Name = newName
	p.Revision = r.rev + 1
	if err := project.Save(p, to); err != nil {
		if target != r.path {
			// Roll the move back so the caller's world is exactly as
			// before the command; surface both failures if even that fails.
			if rb := l.moveForUndo(id, to, from); rb != nil {
				return Entity{}, fmt.Errorf("content: rename edit failed (%v) AND move rollback failed (%v) — manual repair needed", err, rb)
			}
			l.rec[id] = &record{path: from, hash: r.hash, rev: r.rev}
		}
		return Entity{}, fmt.Errorf("content: rename name commit: %w", err)
	}
	afterRaw, aerr := os.ReadFile(to)
	if aerr != nil {
		return Entity{}, fmt.Errorf("content: rename verify: %w", aerr)
	}
	chash, cerr := canonicalHash(p)
	if cerr != nil {
		return Entity{}, cerr
	}
	l.rec[id] = &record{path: to, hash: bytesHash(afterRaw), chash: chash, rev: p.Revision}
	l.pushUndo(entry{
		kind: opRename, id: id, label: "Rename " + filepath.Base(from) + " -> " + filepath.Base(to), prevRev: r.rev,
		fromPath: from, toPath: to, before: beforeRaw, after: afterRaw,
		undo: func() error {
			if err := l.internalWrite(l.locatedPathFor(id, to), beforeRaw); err != nil {
				return err
			}
			if target != r.path {
				return l.moveForUndo(id, to, from)
			}
			return nil
		},
		redo: func() error {
			if target != r.path {
				if err := l.moveForUndo(id, from, to); err != nil {
					return err
				}
			}
			return l.internalWrite(to, afterRaw)
		},
	})
	return l.entityLocked(p, to, false, time.Time{}, ""), nil
}

// locatedPathFor resolves the path an entity currently lives at,
// preferring the live record and falling back to the expected path.
func (l *Library) locatedPathFor(id, fallback string) string {
	if r, ok := l.rec[id]; ok && r.path != "" {
		return r.path
	}
	return fallback
}

// tombstone is the Trash metadata sidecar. deletedAt is parsed from
// DeletedUTC and never serialized itself.
type tombstone struct {
	EntityID   string    `json:"id"`
	DeletedUTC string    `json:"deletedUtc"`
	Reason     string    `json:"reason,omitempty"`
	deletedAt  time.Time // parsed; not serialized
}

func tombstonePath(docPath string) string {
	return docPath + ".meta.json"
}

func (l *Library) writeTombstoneLocked(docPath, id, reason string) error {
	raw, err := json.MarshalIndent(tombstone{EntityID: id, DeletedUTC: time.Now().UTC().Format(time.RFC3339), Reason: reason}, "", "  ")
	if err != nil {
		return fmt.Errorf("content: marshal tombstone: %w", err)
	}
	return os.WriteFile(tombstonePath(docPath), raw, 0o644)
}

func (l *Library) readTombstoneLocked(docPath string) tombstone {
	var t tombstone
	raw, err := os.ReadFile(tombstonePath(docPath))
	if err == nil {
		_ = json.Unmarshal(raw, &t)
	}
	if t.DeletedUTC != "" {
		if ts, err := time.Parse(time.RFC3339, t.DeletedUTC); err == nil {
			t.deletedAt = ts
		}
	}
	return t
}

// findTrashedLocked locates a trashed document by stable ID.
func (l *Library) findTrashedLocked(id string) string {
	if p, ok := l.trashRec[id]; ok {
		return p
	}
	return ""
}

// SoftDelete moves the entity into the Trash area (recoverable,
// keyed by the stable ID). It is a journalled move: the document and
// its tombstone land in .trash together, nothing else changes, and —
// by construction — no file outside the managed library is touched
// (the external source media can never be deleted by this command).
func (l *Library) SoftDelete(id string, expectedRev uint64, reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, r, err := l.checkPreconditions(id, expectedRev)
	if err != nil {
		return err
	}
	if l.renderBlocked(id) {
		return fmt.Errorf("%w: %s", ErrRenderActive, p.Name)
	}
	name := filepath.Base(r.path)
	from, to := r.path, filepath.Join(l.trash, name)
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%w: trash already contains %s", ErrNameTaken, name)
	}
	journal, rollback, jerr := writeJournal(l.root, "trash", id, from, to, p.Revision, p.Revision)
	if jerr != nil {
		return jerr
	}
	if err := os.Rename(from, to); err != nil {
		_ = rollback()
		return fmt.Errorf("content: trash move: %w", err)
	}
	if terr := l.writeTombstoneLocked(to, id, reason); terr != nil {
		// Roll the move back: the live entity must not disappear just
		// because its tombstone failed.
		_ = os.Rename(to, from)
		_ = rollback()
		return terr
	}
	if err := os.Remove(journal); err != nil {
		return fmt.Errorf("content: journal removal: %w", err)
	}
	delete(l.rec, id)
	l.trashRec[id] = to
	l.pushUndo(entry{
		kind: opTrash, id: id, label: "Move to Trash " + p.Name, prevRev: p.Revision,
		fromPath: from, toPath: to,
		undo: func() error { return l.restoreMoveForUndo(id) },
		redo: func() error { return l.trashMoveForUndo(id, "redo of trash") },
	})
	return nil
}

// Restore returns a trashed entity to the live library with its
// ORIGINAL stable ID, name and revision history (§3.12 DELETE/RESTORE).
func (l *Library) Restore(id string) (Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	from := l.findTrashedLocked(id)
	if from == "" {
		return Entity{}, ErrUnknownEntity
	}
	name := filepath.Base(from)
	to := filepath.Join(l.root, name)
	if _, err := os.Lstat(to); err == nil {
		// The original name is taken by a different entity: restore under
		// a deterministic, collision-safe name; identity is unaffected.
		base := strings.TrimSuffix(name, projectExt) + " (restored)"
		safe, serr := safeFileName(l.root, base, projectExt)
		if serr != nil {
			return Entity{}, serr
		}
		to = filepath.Join(l.root, safe)
	}
	journal, rollback, jerr := writeJournal(l.root, "restore", id, from, to, 0, 0)
	if jerr != nil {
		return Entity{}, jerr
	}
	if err := os.Rename(from, to); err != nil {
		_ = rollback()
		return Entity{}, fmt.Errorf("content: restore move: %w", err)
	}
	_ = os.Remove(tombstonePath(from))
	if err := os.Remove(journal); err != nil {
		return Entity{}, fmt.Errorf("content: journal removal: %w", err)
	}
	raw, err := os.ReadFile(to)
	if err != nil {
		return Entity{}, fmt.Errorf("content: verify restore: %w", err)
	}
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Entity{}, fmt.Errorf("content: restored document unparsable: %w", err)
	}
	delete(l.trashRec, id)
	l.rec[id] = &record{path: to, hash: bytesHash(raw), rev: p.Revision}
	l.pushUndo(entry{
		kind: opRestore, id: id, label: "Restore " + p.Name, prevRev: p.Revision,
		fromPath: from, toPath: to,
		undo: func() error { return l.trashMoveForUndo(id, "undo of restore") },
		redo: func() error { return l.restoreMoveForUndo(id) },
	})
	return l.entityLocked(&p, to, false, time.Time{}, ""), nil
}

// Purge permanently deletes an ALREADY TRASHED entity's managed
// document and tombstone. It is a separate explicit command with no
// implicit cascade: only the trashed document inside the library is
// removed; external media, other projects and shared data are out of
// reach by construction (all paths are inside l.trash).
func (l *Library) Purge(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, live := l.rec[id]; live {
		return fmt.Errorf("%w: purge operates on trashed entities only — soft-delete first", ErrNotTrashed)
	}
	from := l.findTrashedLocked(id)
	if from == "" {
		return ErrUnknownEntity
	}
	if l.renderBlocked(id) {
		return fmt.Errorf("%w: %s", ErrRenderActive, id)
	}
	if err := os.Remove(from); err != nil {
		return fmt.Errorf("content: purge %s: %w", id, err)
	}
	_ = os.Remove(tombstonePath(from))
	delete(l.trashRec, id)
	return nil
}

// ImportFile validates an existing project document and persists it as
// a NEW distinct entity (new stable ID) with a collision-safe name.
// Exact byte-identical duplicates are refused with ErrDuplicateContent
// naming the existing entity (reuse instead of silent copy/overwrite
// is the caller's decision). The source file is never modified or
// moved.
func (l *Library) ImportFile(srcPath string) (Entity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return Entity{}, fmt.Errorf("content: import read: %w", err)
	}
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Entity{}, fmt.Errorf("%w: %v", project.ErrCorrupt, err)
	}
	if err := p.Validate(); err != nil {
		return Entity{}, fmt.Errorf("content: import refused: %w", err)
	}
	chash, cerr := canonicalHash(&p)
	if cerr != nil {
		return Entity{}, cerr
	}
	for id, r := range l.rec {
		if r.chash == chash {
			return Entity{}, fmt.Errorf("%w: existing %s", ErrDuplicateContent, id)
		}
	}
	if _, trashed := l.trashIDMatchingCanonical(chash); trashed {
		return Entity{}, fmt.Errorf("%w: an identical entity is in the Trash area (restore it instead)", ErrDuplicateContent)
	}
	id, err := NewID()
	if err != nil {
		return Entity{}, err
	}
	p.ContentID = id
	p.Revision = 1
	name, err := safeFileName(l.root, p.Name, projectExt)
	if err != nil {
		return Entity{}, err
	}
	path := filepath.Join(l.root, name)
	if err := project.Save(&p, path); err != nil {
		return Entity{}, fmt.Errorf("content: import persist: %w", err)
	}
	nraw, err := os.ReadFile(path)
	if err != nil {
		return Entity{}, fmt.Errorf("content: verify import: %w", err)
	}
	l.rec[id] = &record{path: path, hash: bytesHash(nraw), chash: chash, rev: 1}
	l.pushUndo(entry{
		kind: opCreate, id: id, label: "Import " + p.Name, prevRev: 0,
		undo: func() error { return l.trashMoveForUndo(id, "undo of import") },
		redo: func() error { return l.restoreMoveForUndo(id) },
	})
	return l.entityLocked(&p, path, false, time.Time{}, ""), nil
}

// trashIDMatchingCanonical reports whether any TRASHED entity carries
// the given canonical content hash (duplicate awareness across Trash).
func (l *Library) trashIDMatchingCanonical(chash string) (string, bool) {
	for id, path := range l.trashRec {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var p project.Project
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		c, err := canonicalHash(&p)
		if err == nil && c == chash {
			return id, true
		}
	}
	return "", false
}
