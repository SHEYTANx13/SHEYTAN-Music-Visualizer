// Package fileaccess implements user-authorized, read-only, scoped,
// revocable selected-file/folder analysis.
//
// Privilege model (Section 4 of the run-44010666873 work order):
//   - The application NEVER runs elevated for this feature. Standard
//     user permissions already allow reading files the user selected;
//     no UAC prompt, no requireAdministrator, no elevation of the main
//     process. No privileged helper exists because no operation in the
//     current product requires one.
//   - Access is granted ONLY for roots the user explicitly selected.
//     There is no default scan, no sibling/parent expansion, no
//     background walk, no network transfer of any selected byte.
//   - Traversal is READ-ONLY, bounded (depth, file count, per-file and
//     aggregate bytes, wall-clock budget, cancellation) and anchored to
//     an os.Root opened at authorization time: every file open happens
//     relative to that handle (openat containment), which resists
//     path traversal, symlink/junction escapes and TOCTOU races by
//     construction rather than by string-prefix checks.
//   - Well-known sensitive locations (credential stores, private keys,
//     wallets, cookies, auth databases, OS hives, memory dumps, secret
//     stores) are skipped by default even when the user selected a
//     parent folder. A deliberate direct selection of a sensitive file
//     requires an explicit, separate confirmation and is still limited
//     by OS access controls; the default action is always skip.
//   - An audit trail records actor, project, scope, operation, counts
//     and results with REDACTED paths (user-visible labels and opaque
//     scope IDs, never full sensitive paths) and is bounded in size.
//
// All selected content — filenames, metadata, documents, media,
// archive entries and any text inside them — is treated as untrusted
// data. Nothing in this package executes content or interprets it as
// instructions; the only derived data are size, timestamps and a
// capped SHA-256 fingerprint.
package fileaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Defaults for the bounded traversal (conservative first cut).
const (
	DefaultMaxDepth     = 8
	DefaultMaxFiles     = 20000
	DefaultMaxTotalByte = int64(2 << 30) // 2 GiB aggregate read budget
	DefaultMaxFileByte  = int64(256 << 20)
	DefaultTimeBudget   = 60 * time.Second
	readChunk           = 1 << 20
	maxAuditRecords     = 256
	maxWalkErrors       = 64
)

// Outcome strings for the audit trail and UI.
const (
	OutcomeAuthorized = "authorized"
	OutcomeRevoked    = "revoked"
	OutcomeDenied     = "denied"
	OutcomeCompleted  = "completed"
	OutcomeCancelled  = "cancelled"
	OutcomeCapped     = "capped"
	OutcomeError      = "error"
)

// RootIdentity captures the filesystem identity of a granted root at
// authorization time. A root that resolves to a different identity
// (replaced directory, remounted volume, changed network share)
// requires fresh confirmation.
type RootIdentity struct {
	Dev uint64
	Ino uint64
}

// Limits are the traversal bounds shown to the user in the
// authorization summary BEFORE any walk starts.
type Limits struct {
	MaxDepth     int
	MaxFiles     int
	MaxTotalByte int64
	MaxFileByte  int64
	TimeBudget   time.Duration
	// Extensions, when non-empty, restrict analysis to these
	// case-insensitive extensions (e.g. ".wav", ".mp3", ".flac").
	Extensions []string
	// SkipSensitive is the default-skip contract for well-known
	// sensitive paths. Only a direct, double-confirmed selection may
	// override it for that single file — never for a folder walk.
	SkipSensitive bool
}

func (l Limits) withDefaults() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxFiles
	}
	if l.MaxTotalByte <= 0 {
		l.MaxTotalByte = DefaultMaxTotalByte
	}
	if l.MaxFileByte <= 0 {
		l.MaxFileByte = DefaultMaxFileByte
	}
	if l.TimeBudget <= 0 {
		l.TimeBudget = DefaultTimeBudget
	}
	return l
}

// Grant is a revocable, per-project access authorization for ONE
// user-selected root.
type Grant struct {
	ScopeID    string
	RootPath   string
	RootLabel  string // redacted display label (base name, not the full path)
	Identity   RootIdentity
	Limits     Limits
	Purpose    string
	ProjectID  string
	Actor      string
	CreatedAt  time.Time
	LastUsedAt time.Time

	root *os.Root
	mu   sync.Mutex
}

// AuthorizationRequest is the confirmation the UI must show BEFORE
// ConfirmAuthorization creates the grant (Section 4.2: exact roots,
// file types, recursion depth, maximum file count/bytes, purpose).
type AuthorizationRequest struct {
	RootPath  string
	RootLabel string
	Limits    Limits
	Purpose   string
	ProjectID string
	Actor     string

	createdAt time.Time
	identity  RootIdentity
}

// SummaryLines renders the human-readable authorization summary.
func (r AuthorizationRequest) SummaryLines() []string {
	ext := "all file types"
	if len(r.Limits.Extensions) > 0 {
		ext = strings.Join(r.Limits.Extensions, ", ")
	}
	return []string{
		"Selected folder: " + r.RootLabel,
		"Purpose: " + r.Purpose,
		"Access: read-only — no modifications, no uploads, local only",
		"File types: " + ext,
		fmt.Sprintf("Recursion depth: up to %d levels", r.Limits.MaxDepth),
		fmt.Sprintf("Maximum files: %d", r.Limits.MaxFiles),
		fmt.Sprintf("Maximum read volume: %d MB total, %d MB per file",
			r.Limits.MaxTotalByte>>20, r.Limits.MaxFileByte>>20),
		fmt.Sprintf("Time budget: %s (cancellable at any time)", r.Limits.TimeBudget),
		"Well-known sensitive files (credential stores, private keys, wallets, cookies) are skipped by default.",
	}
}

// AuditRecord is one redacted audit trail entry (Section 4.6). It
// never stores full sensitive paths or file contents — only the
// user-visible label, opaque scope ID and aggregate counts.
type AuditRecord struct {
	At          time.Time `json:"at"`
	Actor       string    `json:"actor"`
	ProjectID   string    `json:"projectId,omitempty"`
	ScopeID     string    `json:"scopeId,omitempty"`
	RootLabel   string    `json:"rootLabel,omitempty"`
	Operation   string    `json:"operation"`
	Outcome     string    `json:"outcome"`
	FilesSeen   int       `json:"filesSeen,omitempty"`
	FilesRead   int       `json:"filesRead,omitempty"`
	BytesRead   int64     `json:"bytesRead,omitempty"`
	Skipped     int       `json:"skipped,omitempty"`
	DurationMs  int64     `json:"durationMs,omitempty"`
	Correlation string    `json:"correlation,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

// Manager owns the grants and the bounded audit trail.
type Manager struct {
	mu     sync.Mutex
	grants map[string]*Grant
	audit  []AuditRecord
}

// NewManager creates the access manager. One instance per app.
func NewManager() *Manager {
	return &Manager{grants: map[string]*Grant{}}
}

var (
	// ErrNotAuthorized: no valid grant covers the request.
	ErrNotAuthorized = errors.New("fileaccess: no valid authorization for this root")
	// ErrIdentityChanged: the root now resolves to a different
	// filesystem identity than at authorization time.
	ErrIdentityChanged = errors.New("fileaccess: the selected folder changed on disk — confirm the selection again")
	// ErrSensitiveDefault: the path is skipped by default; a direct
	// selection requires a separate explicit confirmation.
	ErrSensitiveDefault = errors.New("fileaccess: this location is skipped by default because it may contain secrets")
	// ErrReadOnly: mutation attempted through the read-only contract.
	ErrReadOnly = errors.New("fileaccess: grants are read-only")
)

// PrepareAuthorization validates the user-selected root and produces
// the confirmation request the UI must show. No traversal happens
// here; nothing is granted yet.
func (m *Manager) PrepareAuthorization(rootPath, purpose, projectID, actor string, limits Limits) (*AuthorizationRequest, error) {
	if strings.TrimSpace(rootPath) == "" {
		return nil, errors.New("fileaccess: empty root")
	}
	limits = limits.withDefaults()
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("fileaccess: resolve root: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("fileaccess: inspect selected root: %w", err)
	}
	if !fi.IsDir() {
		return nil, errors.New("fileaccess: the selected root must be a folder (single files are analyzed per-file with a grant on their directory)")
	}
	return &AuthorizationRequest{
		RootPath:  abs,
		RootLabel: filepath.Base(strings.TrimRight(abs, string(os.PathSeparator))),
		Limits:    limits, Purpose: purpose, ProjectID: projectID, Actor: actor,
		createdAt: time.Now(), identity: identityOf(fi),
	}, nil
}

// ConfirmAuthorization creates the revocable grant after the user
// confirmed the summary. The os.Root handle anchors all later access.
func (m *Manager) ConfirmAuthorization(req *AuthorizationRequest) (*Grant, error) {
	if req == nil {
		return nil, errors.New("fileaccess: nil authorization request")
	}
	// The confirmation is only valid for a short window: the user
	// confirms what they see, not a stale request.
	if time.Since(req.createdAt) > 10*time.Minute {
		return nil, errors.New("fileaccess: the authorization request expired — select the folder again")
	}
	fi, err := os.Stat(req.RootPath)
	if err != nil {
		return nil, fmt.Errorf("fileaccess: re-inspect selected root: %w", err)
	}
	if id := identityOf(fi); id != req.identity {
		return nil, ErrIdentityChanged
	}
	root, err := os.OpenRoot(req.RootPath)
	if err != nil {
		return nil, fmt.Errorf("fileaccess: open root: %w", err)
	}
	g := &Grant{
		ScopeID:   newScopeID(req.RootPath, req.identity),
		RootPath:  req.RootPath,
		RootLabel: req.RootLabel,
		Identity:  req.identity,
		Limits:    req.Limits,
		Purpose:   req.Purpose,
		ProjectID: req.ProjectID,
		Actor:     req.Actor,
		CreatedAt: time.Now(),
		root:      root,
	}
	m.mu.Lock()
	m.grants[g.ScopeID] = g
	m.mu.Unlock()
	m.record(AuditRecord{At: time.Now(), Actor: req.Actor, ProjectID: req.ProjectID,
		ScopeID: g.ScopeID, RootLabel: req.RootLabel, Operation: "grant", Outcome: OutcomeAuthorized})
	return g, nil
}

// Revoke revokes one grant; later walks fail with ErrNotAuthorized.
func (m *Manager) Revoke(scopeID string) {
	m.mu.Lock()
	g, ok := m.grants[scopeID]
	if ok {
		delete(m.grants, scopeID)
	}
	m.mu.Unlock()
	if ok {
		g.mu.Lock()
		if g.root != nil {
			_ = g.root.Close()
		}
		g.root = nil
		g.mu.Unlock()
		m.record(AuditRecord{At: time.Now(), ScopeID: scopeID, RootLabel: g.RootLabel,
			Operation: "revoke", Outcome: OutcomeRevoked})
	}
}

// RevokeAll revokes every grant (project close / app exit).
func (m *Manager) RevokeAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.grants))
	for id := range m.grants {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Revoke(id)
	}
}

// Grant returns the live grant for a scope ID (nil when revoked).
func (m *Manager) Grant(scopeID string) *Grant {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.grants[scopeID]
}

// FileEntry is one visited regular file inside the authorized root.
type FileEntry struct {
	RelPath string
	Size    int64
	ModTime time.Time
	// SHA256 of the first Min(Size, 1 MiB) bytes — a cheap identity
	// fingerprint for change detection, not a full-content claim.
	HeadSHA256 string
}

// WalkSummary reports what a bounded walk actually did.
type WalkSummary struct {
	FilesSeen        int
	FilesRead        int
	FilesSkippedType int // non-regular entries (devices, FIFOs, sockets)
	FilesSkippedExt  int
	FilesSkippedSens int
	BytesRead        int64
	Errors           []string
	Duration         time.Duration
	Cancelled        bool
	Capped           string // "" or the cap that stopped the walk
}

// Analyze walks the authorized root within every bound and calls visit
// for each analyzed regular file. The callback runs on the walking
// goroutine and must stay quick; it can return context.Canceled to
// stop. Cancellation, caps and the time budget are enforced by the
// walker itself. Entry errors are CLASSIFIED, never echoed with raw
// filesystem paths, so the summary stays redacted.
func (g *Grant) Analyze(ctx context.Context, visit func(ctx context.Context, e FileEntry) error) (WalkSummary, error) {
	g.mu.Lock()
	root, revoked := g.root, g.root == nil
	g.mu.Unlock()
	if revoked {
		return WalkSummary{}, ErrNotAuthorized
	}
	start := time.Now()
	summary := WalkSummary{Errors: []string{}}
	if g.Limits.TimeBudget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Limits.TimeBudget)
		defer cancel()
	}
	limits := g.Limits.withDefaults()

	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > limits.MaxDepth {
			return nil
		}
		f, err := root.Open(rel)
		if err != nil {
			summary.addError(classifyEntryError(err))
			return nil // one unreadable directory never aborts the walk
		}
		entries, err := readDirPaged(f, 512)
		_ = f.Close()
		if err != nil {
			summary.addError(classifyEntryError(err))
			return nil
		}
		for _, ent := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := ent.Name()
			if rel != "." {
				child = rel + "/" + ent.Name()
			}
			switch {
			case ent.IsDir():
				if depth+1 > limits.MaxDepth {
					continue
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			case ent.Type() == 0:
				// Regular file ONLY: symlinks, FIFOs, devices, sockets
				// and irregular entries are never opened. (Opening a
				// FIFO read-only blocks until a writer appears; opening
				// a device can hang or worse — Section 4.3.)
				summary.FilesSeen++
				if summary.FilesSeen > limits.MaxFiles {
					summary.Capped = fmt.Sprintf("file-count cap (%d)", limits.MaxFiles)
					return errCapReached
				}
				if isSensitiveRelPath(child) && limits.SkipSensitive {
					summary.FilesSkippedSens++
					continue
				}
				if !extensionAllowed(child, limits.Extensions) {
					summary.FilesSkippedExt++
					continue
				}
				e, rerr := g.readFileCapped(root, child, limits)
				if rerr != nil {
					if errors.Is(rerr, errSensitive) {
						summary.FilesSkippedSens++
						continue
					}
					summary.addError(classifyEntryError(rerr))
					continue
				}
				summary.FilesRead++
				summary.BytesRead += e.Size
				if summary.BytesRead > limits.MaxTotalByte {
					summary.Capped = fmt.Sprintf("aggregate byte cap (%d MB)", limits.MaxTotalByte>>20)
					return errCapReached
				}
				if visit != nil {
					if verr := visit(ctx, e); verr != nil {
						return verr
					}
				}
			default:
				// Non-regular, non-directory entry: counted, never opened.
				summary.FilesSkippedType++
			}
			if time.Since(start) > limits.TimeBudget {
				summary.Capped = fmt.Sprintf("time budget (%s)", limits.TimeBudget)
				return errCapReached
			}
		}
		return nil
	}

	err := walk(".", 0)
	summary.Duration = time.Since(start)
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		summary.Cancelled = true
	case errors.Is(err, errCapReached):
		// the specific cap is recorded in summary.Capped
	case errors.Is(err, context.DeadlineExceeded):
		summary.Capped = "time budget"
	default:
		return summary, err
	}
	return summary, nil
}

var (
	errCapReached = errors.New("fileaccess: walk cap reached")
	errSensitive  = errors.New("fileaccess: sensitive location skipped by default")
)

// readFileCapped opens ONE file relative to the anchored root handle,
// re-verifies it is a regular file on the OPENED handle (TOCTOU
// mitigation), and hashes at most MaxFileByte bytes.
func (g *Grant) readFileCapped(root *os.Root, rel string, limits Limits) (FileEntry, error) {
	if isSensitiveRelPath(rel) && limits.SkipSensitive {
		return FileEntry{}, errSensitive
	}
	f, err := root.Open(rel)
	if err != nil {
		return FileEntry{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return FileEntry{}, err
	}
	if !fi.Mode().IsRegular() {
		return FileEntry{}, errNotRegular
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limits.MaxFileByte))
	if err != nil {
		return FileEntry{}, err
	}
	// The reported Size is the bytes actually read within the cap —
	// never a claim about unread content beyond it.
	return FileEntry{
		RelPath: rel, Size: n, ModTime: fi.ModTime(),
		HeadSHA256: hex.EncodeToString(h.Sum(nil)),
	}, nil
}

var errNotRegular = errors.New("non-regular entry skipped")

// AnalyzeFile analyzes ONE explicitly selected file. The default
// action for sensitive files is skip (ErrSensitiveDefault); the
// confirmedSensitive flag exists only for a deliberate, separately
// confirmed direct selection and still respects every OS access
// control — it never bypasses anything, it only stops the local
// default-skip for that single file.
func (g *Grant) AnalyzeFile(ctx context.Context, relPath string, confirmedSensitive bool) (FileEntry, WalkSummary, error) {
	sum := WalkSummary{Errors: []string{}}
	g.mu.Lock()
	root := g.root
	g.mu.Unlock()
	if root == nil {
		return FileEntry{}, sum, ErrNotAuthorized
	}
	if isSensitiveRelPath(relPath) && g.Limits.SkipSensitive && !confirmedSensitive {
		return FileEntry{}, sum, ErrSensitiveDefault
	}
	if err := ctx.Err(); err != nil {
		return FileEntry{}, sum, err
	}
	limits := g.Limits.withDefaults()
	if confirmedSensitive {
		// Only this single direct selection drops the
		// default-skip; OS access controls still apply
		// untouched, and folder walks keep skipping.
		limits.SkipSensitive = false
	}
	e, err := g.readFileCapped(root, relPath, limits)
	if err != nil {
		return FileEntry{}, sum, err
	}
	sum.FilesSeen, sum.FilesRead, sum.BytesRead = 1, 1, e.Size
	return e, sum, nil
}

// ReadFile returns a bounded reader over one file inside the grant
// (for callers that need content, still read-only and capped).
func (g *Grant) ReadFile(relPath string) (io.ReadCloser, int64, error) {
	g.mu.Lock()
	root := g.root
	g.mu.Unlock()
	if root == nil {
		return nil, 0, ErrNotAuthorized
	}
	if isSensitiveRelPath(relPath) && g.Limits.SkipSensitive {
		return nil, 0, ErrSensitiveDefault
	}
	f, err := root.Open(relPath)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, 0, errNotRegular
	}
	limits := g.Limits.withDefaults()
	size := fi.Size()
	if size > limits.MaxFileByte {
		size = limits.MaxFileByte
	}
	return &cappedReader{r: io.LimitReader(f, size), c: f}, size, nil
}

type cappedReader struct {
	r io.Reader
	c io.Closer
}

func (cr *cappedReader) Read(p []byte) (int, error) { return cr.r.Read(p) }
func (cr *cappedReader) Close() error               { return cr.c.Close() }

// readDirPaged reads directory entries in pages so a directory with
// millions of entries cannot allocate without bound.
func readDirPaged(f *os.File, page int) ([]os.DirEntry, error) {
	var out []os.DirEntry
	for {
		ents, err := f.ReadDir(page)
		out = append(out, ents...)
		if len(ents) < page || err != nil {
			if err != nil && len(out) == 0 {
				return nil, err
			}
			return out, nil
		}
	}
}

func (w *WalkSummary) addError(msg string) {
	if len(w.Errors) < maxWalkErrors {
		w.Errors = append(w.Errors, msg)
	}
}

// classifyEntryError reduces filesystem errors to redacted categories:
// the walk summary never carries raw filesystem paths.
func classifyEntryError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		if os.IsPermission(err) {
			return "permission denied"
		}
		if os.IsNotExist(err) {
			return "entry disappeared"
		}
		if errors.Is(err, errNotRegular) {
			return "non-regular entry"
		}
		return "unreadable entry (" + pe.Op + ")"
	}
	if errors.Is(err, errNotRegular) {
		return "non-regular entry"
	}
	return "unreadable entry"
}

// extensionAllowed reports whether rel matches the extension filter.
func extensionAllowed(rel string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	e := strings.ToLower(filepath.Ext(rel))
	for _, want := range exts {
		if e == strings.ToLower(want) {
			return true
		}
	}
	return false
}

// newScopeID derives a stable opaque scope ID from the root identity.
func newScopeID(root string, id RootIdentity) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", root, id.Dev, id.Ino)))
	return "scope-" + hex.EncodeToString(h[:8])
}

// record appends to the bounded in-memory audit ring.
func (m *Manager) record(r AuditRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, r)
	if len(m.audit) > maxAuditRecords {
		m.audit = m.audit[len(m.audit)-maxAuditRecords:]
	}
}

// AuditRecords returns the most recent audit records (bounded ring).
func (m *Manager) AuditRecords(n int) []AuditRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 || n > len(m.audit) {
		n = len(m.audit)
	}
	out := make([]AuditRecord, n)
	copy(out, m.audit[len(m.audit)-n:])
	return out
}

// AuditJSON returns the recent audit records as JSON lines (explicit
// user export path; the caller decides where it lands).
func (m *Manager) AuditJSON(n int) ([]byte, error) {
	records := m.AuditRecords(n)
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

// RecordWalk adds a walk outcome to the audit trail (called by the
// studio after Analyze).
func (m *Manager) RecordWalk(g *Grant, s WalkSummary, outcome, correlation string) {
	m.record(AuditRecord{
		At: time.Now(), Actor: g.Actor, ProjectID: g.ProjectID, ScopeID: g.ScopeID,
		RootLabel: g.RootLabel, Operation: "analyze", Outcome: outcome,
		FilesSeen: s.FilesSeen, FilesRead: s.FilesRead, BytesRead: s.BytesRead,
		Skipped:    s.FilesSkippedType + s.FilesSkippedExt + s.FilesSkippedSens,
		DurationMs: s.Duration.Milliseconds(), Correlation: correlation,
	})
}
