// Package content is the single UI-independent, validated command
// boundary for persisted project content (run-43767556517 milestone
// §3.12). It owns stable typed identities, optimistic revision checks,
// external-modification detection, transactional multi-step operations
// with a crash-recovery journal, soft-delete/restore with a project
// Trash area, duplicate-aware import, and a bounded command-based
// undo/redo stack.
//
// Design contract:
//
//   - Identity: every persisted entity carries an immutable typed ID
//     ("prj-" + 26 lowercase crockford-base32 chars) stored INSIDE the
//     existing project document (additive schema fields contentId and
//     revision; old builds ignore them, this build assigns them on
//     first write). IDs never depend on file names, list indexes,
//     display names, paths or widget state.
//   - Single source of truth: the library directory of .sheytan.json
//     documents, parsed with the existing internal/project format. No
//     second store, no index that can drift — listings are derived
//     from the directory itself.
//   - Atomicity: every persisted mutation is one logical transaction.
//     Single-file changes use temp-file + fsync + atomic rename
//     (internal/project.Save). Multi-step file moves (rename/soft-
//     delete/restore) are guarded by a JSON journal that a later Open
//     replays deterministically to finish or roll back, so a crash can
//     only leave the complete old or the complete new state.
//   - Concurrency: every mutating command carries the expected
//     revision AND is verified against the document bytes hash that
//     the library last observed; stale or externally overwritten
//     documents are rejected with typed errors instead of last-write-
//     wins.
//   - Delete safety: Remove-from-library is soft (Trash area), keyed
//     by the stable ID, and NEVER touches the external source media —
//     deleting a project reference can never delete the song on disk.
//     Permanent Purge is a separate explicit command operating only on
//     already-trashed entities. A render guard hook lets the host
//     refuse deletion of content an active render still consumes.
//   - Undo/redo: bounded, revision-scoped command inverses; an undo
//     that would act on changed state is refused with an explanation
//     rather than corrupting anything.
//
// The service never imports UI packages and never touches widget state;
// the UI consumes returned immutable entities/events only.
package content

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/project"
)

// Errors surfaced to callers (UI maps them to exact dialogs).
var (
	// ErrUnknownEntity: no live entity with this ID.
	ErrUnknownEntity = errors.New("content: unknown entity id")
	// ErrEntityTrashed: the ID exists but only inside the Trash area.
	ErrEntityTrashed = errors.New("content: entity is in the Trash area")
	// ErrRevisionConflict: the expected revision does not match the
	// persisted one (a newer edit exists; never last-write-wins).
	ErrRevisionConflict = errors.New("content: stale revision (the entity changed since it was loaded)")
	// ErrExternalModification: the document bytes changed outside this
	// Library since they were last observed.
	ErrExternalModification = errors.New("content: document was modified outside the library")
	// ErrNameTaken: rename target exists (never silently overwrite).
	ErrNameTaken = errors.New("content: target name already exists")
	// ErrRenderActive: the render guard refused the destructive command.
	ErrRenderActive = errors.New("content: entity is in use by an active render")
	// ErrDuplicateContent: an identical document already exists.
	ErrDuplicateContent = errors.New("content: identical document already exists (fingerprint match)")
	// ErrInvalidName: empty or filesystem-unsafe display name.
	ErrInvalidName = errors.New("content: invalid entity name")
	// ErrNotTrashed: purge/restore requested on a live entity.
	ErrNotTrashed = errors.New("content: entity is not in the Trash area")
	// ErrUndoUnavailable: no applicable undo/redo record (empty stack or
	// record invalidated by later state).
	ErrUndoUnavailable = errors.New("content: undo/redo unavailable for the current state")
)

// contentIDPattern is the canonical typed identity format.
var contentIDPattern = regexp.MustCompile(`^prj-[0-9a-v]{26}$`)

// ValidID reports whether id has the canonical typed format.
func ValidID(id string) bool { return contentIDPattern.MatchString(id) }

// NewID mints a fresh typed identity from the crypto random source.
// The alphabet is crockford base32 (lowercase); 128 bits of entropy
// are encoded into the 26 payload characters.
func NewID() (string, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuv"
	var raw [17]byte // 17 * 8 bits ≈ 136 bits of entropy
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("content: id entropy: %w", err)
	}
	var b strings.Builder
	b.WriteString("prj-")
	bits := 0
	var cur uint64
	for _, by := range raw {
		cur = cur<<8 | uint64(by)
		bits += 8
		for bits >= 5 && b.Len() < 4+26 {
			shift := uint(bits - 5)
			b.WriteByte(alphabet[(cur>>shift)&31])
			bits -= 5
		}
		if b.Len() == 4+26 {
			break
		}
	}
	id := b.String()
	if !ValidID(id) {
		return "", fmt.Errorf("content: generated id failed validation: %q", id)
	}
	return id, nil
}

// Entity is an immutable snapshot of one persisted library entry.
// Callers (UI, renderer) treat it as read-only state; every mutation
// goes back through the Library commands.
type Entity struct {
	ID           string    // stable typed ID (never changes)
	Revision     uint64    // optimistic-concurrency token
	Name         string    // display name (renameable)
	Path         string    // absolute document path (managed; renameable)
	Trashed      bool      // lives in the Trash area
	DeletedAt    time.Time // zero for live entities
	DeleteReason string
	CreatedAt    time.Time
	ModifiedAt   time.Time

	// Media reference summary (external source is NEVER owned here).
	MediaFileName string
	Fingerprint   string
	Duration      float64
}

// Document returns the validated project document for the entity.
// The returned value is a detached copy: persisted mutations go back
// through Library commands only.
func (e Entity) Document() (*project.Project, error) {
	p, err := project.Load(e.Path)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// bytesHash is the external-modification sentinel: the library records
// the SHA-256 of the document bytes it last observed and compares it
// before every mutation.
func bytesHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalHash identifies SEMANTIC duplicate content: the SHA-256 of
// the validated document with the service-owned identity fields
// (contentId/revision) stripped, so an imported legacy document and its
// already-imported library copy compare equal.
func canonicalHash(p *project.Project) (string, error) {
	cp := *p
	cp.ContentID = ""
	cp.Revision = 0
	b, err := json.Marshal(&cp)
	if err != nil {
		return "", fmt.Errorf("content: canonical marshal: %w", err)
	}
	return bytesHash(b), nil
}

// safeFileName maps a display name to a collision-free managed file
// name. It preserves the original characters (Unicode, spaces), maps
// filesystem-hostile characters deterministically, trims platform-
// hostile trailing dots/spaces, and appends " (2)", " (3)"… on
// collision. It never overwrites an existing file.
func safeFileName(dir, base, ext string) (string, error) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '-' // filesystem-hostile on some platform
		}
		return r
	}, strings.TrimSpace(base))
	clean = strings.TrimRight(clean, ". ")
	if clean == "" || clean == "." || clean == ".." {
		return "", ErrInvalidName
	}
	candidate := clean + ext
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("content: probe name: %w", err)
		}
		candidate = fmt.Sprintf("%s (%d)%s", clean, i, ext)
		if i > 9999 {
			return "", fmt.Errorf("content: no free name for %q", base)
		}
	}
}
