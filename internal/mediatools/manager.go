package mediatools

// manager.go — the in-app Media Tools & Codecs manager (run
// 43712071111). One Manager per process owns the app-managed tool
// directory; the UI subscribes to state snapshots and never touches
// downloader internals. Install flow: pinned HTTPS download (host
// allowlisted, size bounded, streamed into a unique temp file while
// hashing) -> pinned SHA-256 verification BEFORE extraction -> bounded
// traversal-safe extraction into a unique staging directory -> pack
// verification (identity, license configuration, required encoders,
// ffprobe, encode+probe smoke test) -> atomic swap with rollback (the
// previous known-good installation is preserved until the new one is
// verified; a failure restores it). Parallel installs are refused by a
// single in-flight lock. Everything is cancellable through the
// context; temp artifacts are cleaned on success, failure and
// cancellation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// State is the manager's honest state machine.
type State string

const (
	StateNotInstalled        State = "not-installed"
	StateReady               State = "ready"
	StateUpdateAvailable     State = "update-available"
	StateRepairNeeded        State = "repair-needed"
	StateUnsupportedPlatform State = "unsupported-platform"
	StateFailed              State = "failed"
	StateDownloading         State = "downloading"
	StateVerifying           State = "verifying"
	StateInstalling          State = "installing"
)

// MaxDownloadBytes bounds the compressed download.
const MaxDownloadBytes = 512 << 20 // 512 MiB

// allowedDownloadHosts is the hard allowlist for redirects: the pinned
// GitHub release assets redirect to GitHub's CDN.
var allowedDownloadHosts = map[string]bool{
	"github.com": true, "objects.githubusercontent.com": true, "codeload.github.com": true,
}

// Snapshot is an immutable progress/state report for the UI.
type Snapshot struct {
	State          State          `json:"state"`
	Stage          string         `json:"stage,omitempty"`
	Detail         string         `json:"detail,omitempty"`
	Downloaded     int64          `json:"downloadedBytes"`
	Total          int64          `json:"totalBytes"`
	BytesPerSecond float64        `json:"bytesPerSecond"`
	Installed      *InstalledInfo `json:"installed,omitempty"`
	LastError      string         `json:"lastError,omitempty"`
	Pack           *PlatformPack  `json:"pack,omitempty"`
}

// Options configures a Manager (testing hooks are unexported fields).
type Options struct {
	DataRoot string
	Logger   *slog.Logger
	client   *http.Client
	manifest func() (Manifest, error)
	verify   func(PlatformPack, string) (VerifyResult, error)
}

// Manager owns the app-managed media tools directory.
type Manager struct {
	opts Options
	pack PlatformPack
	err  error // unsupported platform

	mu       sync.Mutex
	state    State
	detail   string
	stage    string
	lastErr  string
	inFlight bool
	cancel   context.CancelFunc

	progMu            sync.Mutex
	downloaded, total int64
	bps               float64

	subMu sync.Mutex
	subs  []func(Snapshot)
}

// New builds the manager for the current platform. New never performs
// I/O: the pack is resolved from the embedded manifest and the state
// is derived lazily (or by CheckForUpdate/Install).
func New(opts Options) *Manager {
	m := &Manager{opts: opts, state: StateNotInstalled}
	if opts.manifest == nil {
		opts.manifest = func() (Manifest, error) { return EmbeddedManifest(), nil }
	}
	if opts.client == nil {
		opts.client = &http.Client{Timeout: 30 * time.Minute, CheckRedirect: checkRedirect}
	}
	if opts.verify == nil {
		opts.verify = verifyPack
	}
	m.opts = opts
	manifest, err := opts.manifest()
	if err != nil {
		m.err = err
		m.state = StateFailed
		m.lastErr = err.Error()
		return m
	}
	pack, perr := PackFor(manifest, runtimeGOOS, runtimeArch)
	if perr != nil {
		m.err = perr
		m.state = StateUnsupportedPlatform
		m.detail = perr.Error()
		return m
	}
	m.pack = pack
	if installed, ierr := InspectInstalled(opts.DataRoot); ierr == nil {
		m.state = StateReady
		m.detail = installed.Manifest.Version
	} else if instErr := inspectMissingReason(ierr); instErr == repair {
		m.state = StateRepairNeeded
		m.detail = ierr.Error()
	}
	return m
}

// repair/missing discrimination for the initial state.
const (
	missing = iota
	repair
)

func inspectMissingReason(err error) int {
	msg := err.Error()
	if strings.Contains(msg, "no app-managed tool pair") {
		return missing
	}
	return repair // manifest unreadable/invalid while the pair exists
}

// checkRedirect enforces HTTPS and the host allowlist for redirects.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("mediatools: refusing non-HTTPS redirect to %s", req.URL.Scheme)
	}
	if !allowedDownloadHosts[req.URL.Host] {
		return fmt.Errorf("mediatools: refusing redirect to unapproved host %s", req.URL.Host)
	}
	if len(via) >= 5 {
		return errors.New("mediatools: too many redirects")
	}
	return nil
}

// Snapshot returns the current state snapshot.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{State: m.state, Stage: m.stage, Detail: m.detail, LastError: m.lastErr}
	m.progMu.Lock()
	s.Downloaded, s.Total, s.BytesPerSecond = m.downloaded, m.total, m.bps
	m.progMu.Unlock()
	if s.State == StateReady || s.State == StateRepairNeeded || s.State == StateUpdateAvailable {
		if installed, err := InspectInstalled(m.opts.DataRoot); err == nil {
			s.Installed = installed
		}
	}
	if m.pack.Version != "" {
		p := m.pack
		s.Pack = &p
	}
	return s
}

// Subscribe registers a snapshot listener (called from the manager's
// caller goroutine on every state/progress change; the UI marshals via
// the shell event loop).
func (m *Manager) Subscribe(fn func(Snapshot)) {
	m.subMu.Lock()
	m.subs = append(m.subs, fn)
	m.subMu.Unlock()
}

func (m *Manager) notify() {
	s := m.Snapshot()
	m.subMu.Lock()
	subs := append([]func(Snapshot){}, m.subs...)
	m.subMu.Unlock()
	for _, fn := range subs {
		fn(s)
	}
}

// Cancel aborts an in-flight install/update.
func (m *Manager) Cancel() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// CheckForUpdate re-inspects the installed pack against the pinned
// manifest and repairs the state (ready / update-available /
// repair-needed). It performs no downloads.
func (m *Manager) CheckForUpdate(ctx context.Context) (Snapshot, error) {
	if m.err != nil {
		return m.Snapshot(), m.err
	}
	installed, err := InspectInstalled(m.opts.DataRoot)
	m.mu.Lock()
	switch {
	case err != nil && inspectMissingReason(err) == repair:
		m.state = StateRepairNeeded
		m.detail = err.Error()
	case err != nil:
		m.state = StateNotInstalled
		m.detail = ""
	default:
		if installed.Manifest.Version != m.pack.Version || installed.Manifest.SHA256 != m.pack.SHA256 {
			m.state = StateUpdateAvailable
			m.detail = "installed " + installed.Manifest.Version + " -> pinned " + m.pack.Version
		} else {
			m.state = StateReady
			m.detail = installed.Manifest.Version
		}
	}
	m.mu.Unlock()
	m.notify()
	return m.Snapshot(), nil
}

// Install downloads, verifies and installs the pinned pack. It blocks
// until completion; call it from a worker goroutine with a context the
// UI can cancel. A verified existing installation is only replaced
// after the new pack passes full verification (rollback on failure).
func (m *Manager) Install(ctx context.Context) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	if m.inFlight {
		m.mu.Unlock()
		return errors.New("mediatools: an install or update is already in progress")
	}
	m.inFlight = true
	ctx, m.cancel = context.WithCancel(ctx)
	// remember the stable state to restore on cancellation/failure
	prevState, prevDetail := m.state, m.detail
	// Run 46168717789 smart-download contract: a pinned pack that is
	// ALREADY installed (version + digest identity in current/manifest
	// + the pair on disk) must never be re-downloaded. Zero bytes are
	// transferred; the state flips straight to Ready.
	if info, ierr := InspectInstalled(m.opts.DataRoot); ierr == nil &&
		info.Manifest.Version == m.pack.Version && info.Manifest.SHA256 == m.pack.SHA256 {
		m.inFlight = false
		m.cancel = nil
		m.state = StateReady
		m.stage = ""
		m.detail = fmt.Sprintf("already current: pinned pack %s is installed (digest-verified at install time) — no download needed", m.pack.Version)
		m.lastErr = ""
		m.mu.Unlock()
		m.notify()
		return nil
	}
	m.state = StateDownloading
	m.stage = "download"
	m.mu.Unlock()
	cleanup := func(final State, detail, lastErr string) {
		m.mu.Lock()
		m.inFlight = false
		m.cancel = nil
		m.stage = ""
		m.state = final
		m.detail = detail
		m.lastErr = lastErr
		m.mu.Unlock()
		m.progMu.Lock()
		m.downloaded, m.total, m.bps = 0, 0, 0
		m.progMu.Unlock()
		m.notify()
	}
	defer func() {
		if r := recover(); r != nil {
			cleanup(StateFailed, "install panicked", fmt.Sprint(r))
			panic(r) // the caller's panic boundary decides; never swallow silently
		}
	}()

	tempArchive, err := m.download(ctx)
	if err != nil {
		_ = os.RemoveAll(tempArchive)
		if ctx.Err() != nil {
			cleanup(prevState, prevDetail, "")
			return fmt.Errorf("mediatools: install cancelled: %w", ctx.Err())
		}
		cleanup(StateFailed, "", err.Error())
		return err
	}
	defer os.RemoveAll(filepath.Dir(tempArchive))

	m.mu.Lock()
	m.state = StateVerifying
	m.stage = "verify-digest"
	m.mu.Unlock()
	m.notify()

	if err := m.verifyDigest(tempArchive); err != nil {
		cleanup(StateFailed, "", err.Error())
		return err
	}

	m.mu.Lock()
	m.state = StateInstalling
	m.stage = "extract"
	m.mu.Unlock()
	m.notify()

	if err := m.installVerified(ctx, tempArchive); err != nil {
		if errors.Is(err, errRolledBack) {
			cleanup(StateRepairNeeded, "previous installation restored", err.Error())
			return err
		}
		if ctx.Err() != nil {
			cleanup(prevState, prevDetail, "")
			return fmt.Errorf("mediatools: install cancelled: %w", ctx.Err())
		}
		cleanup(StateFailed, "", err.Error())
		return err
	}
	cleanup(StateReady, m.pack.Version, "")
	return nil
}

// errRolledBack marks a swap failure where the previous known-good
// installation was restored.
var errRolledBack = errors.New("mediatools: install failed; previous installation restored")

// download streams the pinned asset into a unique temp directory,
// hashing while downloading, enforcing the size bound.
func (m *Manager) download(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.pack.URL, nil)
	if err != nil {
		return "", fmt.Errorf("mediatools: request: %w", err)
	}
	resp, err := m.opts.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mediatools: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mediatools: download: HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(Root(m.opts.DataRoot), 0o755); err != nil {
		return "", fmt.Errorf("mediatools: tools root: %w", err)
	}
	tmpDir, err := os.MkdirTemp(Root(m.opts.DataRoot), ".dl-")
	if err != nil {
		return "", fmt.Errorf("mediatools: temp dir: %w", err)
	}
	tmp := filepath.Join(tmpDir, "pack."+m.pack.ArchiveFormat)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("mediatools: temp file: %w", err)
	}
	hasher := sha256.New()
	m.progMu.Lock()
	m.total = resp.ContentLength
	m.downloaded = 0
	m.bps = 0
	m.progMu.Unlock()

	var written int64
	start := time.Now()
	buf := make([]byte, 64<<10)
	lastNotify := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			return tmp, err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > MaxDownloadBytes {
				out.Close()
				return tmp, fmt.Errorf("mediatools: download exceeds %d bytes bound", MaxDownloadBytes)
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return tmp, fmt.Errorf("mediatools: write download: %w", werr)
			}
			hasher.Write(buf[:n])
			m.progMu.Lock()
			m.downloaded = written
			elapsed := time.Since(start).Seconds()
			if elapsed > 0.25 {
				m.bps = float64(written) / elapsed
			}
			m.progMu.Unlock()
			if time.Since(lastNotify) > 250*time.Millisecond {
				lastNotify = time.Now()
				m.notify()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return tmp, fmt.Errorf("mediatools: read download: %w", rerr)
		}
	}
	if err := out.Close(); err != nil {
		return tmp, fmt.Errorf("mediatools: close download: %w", err)
	}
	m.notify()
	return tmp, nil
}

// verifyDigest compares the downloaded archive against the pinned
// SHA-256 (computed during the streamed download).
func (m *Manager) verifyDigest(archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("mediatools: open archive: %w", err)
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("mediatools: hash archive: %w", err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != m.pack.SHA256 {
		return fmt.Errorf("mediatools: digest mismatch: got %s, pinned %s", got, m.pack.SHA256)
	}
	return nil
}

// installVerified extracts, verifies and atomically swaps the new
// pack into place with rollback.
func (m *Manager) installVerified(ctx context.Context, archivePath string) error {
	if err := os.MkdirAll(Root(m.opts.DataRoot), 0o755); err != nil {
		return fmt.Errorf("mediatools: tools root: %w", err)
	}
	stageDir, err := os.MkdirTemp(Root(m.opts.DataRoot), ".tmp-")
	if err != nil {
		return fmt.Errorf("mediatools: staging dir: %w", err)
	}
	defer os.RemoveAll(stageDir)

	opt := ExtractOptions{Pack: m.pack, Dest: stageDir, Goos: runtimeGOOS}
	switch m.pack.ArchiveFormat {
	case "zip":
		_, err = extractZip(archivePath, opt)
	case "tar.xz":
		_, err = extractTarXZ(archivePath, opt)
	default:
		return fmt.Errorf("mediatools: unsupported archive format %q", m.pack.ArchiveFormat)
	}
	if err != nil {
		return err
	}
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}

	m.mu.Lock()
	m.stage = "verify-pack"
	m.mu.Unlock()
	m.notify()

	vres, err := m.opts.verify(m.pack, filepath.Join(stageDir, filepath.Dir(filepath.ToSlash(m.pack.FFmpegPath))))
	if err != nil {
		return err
	}

	// Materialize the final layout inside the staging dir: bin/ pair,
	// manifest.json and the licenses/ provenance copies.
	final := filepath.Join(stageDir, "final")
	binFinal := filepath.Join(final, "bin")
	if err := os.MkdirAll(binFinal, 0o755); err != nil {
		return fmt.Errorf("mediatools: final bin dir: %w", err)
	}
	ff, fp := BinNames(runtimeGOOS)
	srcBin := filepath.Join(stageDir, filepath.ToSlash(filepath.Dir(m.pack.FFmpegPath)))
	for _, name := range []string{ff, fp} {
		if err := copyFile(filepath.Join(final, "bin", name), filepath.Join(srcBin, name), 0o755); err != nil {
			return err
		}
	}
	im := InstalledManifest{
		Platform:    m.pack.Platform,
		Version:     m.pack.Version,
		URL:         m.pack.URL,
		SHA256:      m.pack.SHA256,
		License:     m.pack.LicenseClass,
		SourceURL:   m.pack.SourceURL,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Encoders:    vres.Encoders,
		Pack:        m.pack,
	}
	raw, err := json.MarshalIndent(im, "", "  ")
	if err != nil {
		return fmt.Errorf("mediatools: marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(final, "manifest.json"), raw, 0o644); err != nil {
		return fmt.Errorf("mediatools: write manifest: %w", err)
	}
	if err := writeLicenseFiles(m.pack, filepath.Join(final, "licenses"), stageDir); err != nil {
		return err
	}

	// Atomic swap with rollback: previous <- current (kept as the
	// known-good), current <- final.
	_ = os.RemoveAll(PreviousDir(m.opts.DataRoot))
	if _, statErr := os.Stat(CurrentDir(m.opts.DataRoot)); statErr == nil {
		if err := os.Rename(CurrentDir(m.opts.DataRoot), PreviousDir(m.opts.DataRoot)); err != nil {
			return fmt.Errorf("mediatools: preserve previous installation: %w", err)
		}
	}
	if err := os.Rename(final, CurrentDir(m.opts.DataRoot)); err != nil {
		// Rollback: restore the previous installation.
		if _, prevErr := os.Stat(PreviousDir(m.opts.DataRoot)); prevErr == nil {
			if rbErr := os.Rename(PreviousDir(m.opts.DataRoot), CurrentDir(m.opts.DataRoot)); rbErr != nil {
				return fmt.Errorf("mediatools: swap failed (%v) AND rollback failed (%v) — manual repair needed", err, rbErr)
			}
		}
		return fmt.Errorf("%w: %v", errRolledBack, err)
	}
	_ = os.RemoveAll(PreviousDir(m.opts.DataRoot))
	return nil
}

// writeLicenseFiles copies archive license documents and writes the
// provenance record the UI links to.
func writeLicenseFiles(pack PlatformPack, dst, stageDir string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mediatools: licenses dir: %w", err)
	}
	// Copy license documents found in the archive (already materialized
	// at the staging root by the extraction filter).
	_ = filepath.Walk(stageDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || p == dst || strings.HasPrefix(p, dst+string(os.PathSeparator)) {
			return nil
		}
		base := strings.ToLower(info.Name())
		if strings.HasPrefix(base, "copying") || strings.HasPrefix(base, "license") || strings.HasPrefix(base, "notice") {
			_ = copyFile(filepath.Join(dst, info.Name()), p, 0o644)
		}
		return nil
	})
	prov := fmt.Sprintf(`SHEYTAN Music Visualizer — app-managed media tool pack (provenance)

Platform:       %s
Version:        %s
Upstream asset: %s
SHA-256:        %s
License class:  %s
Source project: %s
Build release:  %s
License notice: %s

This tool pack was downloaded and verified by the SHEYTAN Media Tools &
Codecs manager (pinned manifest; digest verified before extraction).
The SHEYTAN application itself contains no FFmpeg code and ships no
FFmpeg binaries; the GPL applies to this optional tool pack only.
`, pack.Platform, pack.Version, pack.URL, pack.SHA256, pack.LicenseClass, pack.SourceURL, pack.BuildURL, pack.LicenseNotice)
	if err := os.WriteFile(filepath.Join(dst, "PROVENANCE.txt"), []byte(prov), 0o644); err != nil {
		return fmt.Errorf("mediatools: write provenance: %w", err)
	}
	return nil
}

// Remove deletes the app-managed installation (user data only; a
// system FFmpeg on PATH or an app-local portable pair is untouched).
func (m *Manager) Remove() error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	if m.inFlight {
		m.mu.Unlock()
		return errors.New("mediatools: cannot remove while an install is in progress")
	}
	m.mu.Unlock()
	_ = os.RemoveAll(PreviousDir(m.opts.DataRoot))
	if err := os.RemoveAll(CurrentDir(m.opts.DataRoot)); err != nil {
		return fmt.Errorf("mediatools: remove: %w", err)
	}
	m.mu.Lock()
	m.state = StateNotInstalled
	m.detail = ""
	m.mu.Unlock()
	m.notify()
	return nil
}

// ArchiveURL exposes the pinned asset URL (UI provenance display).
func (m *Manager) ArchiveURL() string { return m.pack.URL }

// ManifestURLString identifies the manifest source (embedded).
func (m *Manager) ManifestURLString() string {
	return "embedded manifest (internal/mediatools/manifest.json)"
}

// PackInfo exposes the pinned pack (UI display).
func (m *Manager) PackInfo() PlatformPack { return m.pack }

func copyFile(dst, src string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("mediatools: copy %s: %w", filepath.Base(src), err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("mediatools: copy to %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("mediatools: copy %s: %w", filepath.Base(src), err)
	}
	return nil
}

// runtimeArch is a testing indirection for runtime.GOARCH (tests may
// pin it to exercise platform selection on any host).
var runtimeArch = runtime.GOARCH
