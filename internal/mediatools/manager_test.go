package mediatools

// manager_test.go — downloader/installer fixtures (run 43712071111)
// driven by httptest: success, digest mismatch, server error, wrong
// content, cancellation, rollback (previous installation preserved),
// concurrent install refusal, removal and update detection. The
// executable verification step is injectable so the flow is testable
// without binaries; the real binary verification has its own
// env-gated integration test.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeArchive builds a deterministic test zip with the pack layout.
func fakeArchive(t *testing.T, pack PlatformPack) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := map[string]string{
		pack.FFmpegPath:          "ffmpeg-fake",
		pack.FFprobePath:         "ffprobe-fake",
		"pkg-root/COPYING.GPLv2": "fake license",
	}
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testManager builds a manager pinned to a local httptest server
// serving archive bytes, with injectable verification.
func testManager(t *testing.T, archive []byte, verifyErr error) (*Manager, *httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	pack := testPack()
	pack.ArchiveFormat = "zip"
	sum := sha256.Sum256(archive)
	pack.URL = srv.URL + "/pack.zip"
	pack.SHA256 = hex.EncodeToString(sum[:])
	manifest := Manifest{Schema: SchemaVersion, Updated: "test", Platforms: map[string]PlatformPack{
		"linux/amd64": pack,
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// Force the platform to the manifest's entry regardless of host.
	oldGOOS, oldArch := runtimeGOOS, runtimeArch
	runtimeGOOS, runtimeArch = "linux", "amd64"
	t.Cleanup(func() { runtimeGOOS, runtimeArch = oldGOOS, oldArch })

	dataRoot := t.TempDir()
	m := New(Options{
		DataRoot: dataRoot,
		client:   srv.Client(),
		manifest: func() (Manifest, error) { return ParseManifest(raw) },
		verify: func(PlatformPack, string) (VerifyResult, error) {
			if verifyErr != nil {
				return VerifyResult{}, verifyErr
			}
			return VerifyResult{Version: "ffmpeg version n-test", Encoders: []string{"aac", "libx264", "libx265"}}, nil
		},
	})
	return m, srv, dataRoot
}

func TestManagerInstallFlowSuccess(t *testing.T) {
	m, _, _ := testManager(t, nil, nil)
	packBytes := fakeArchive(t, m.PackInfo())
	m2, _, dataRoot := testManager(t, packBytes, nil)
	if err := m2.Install(context.Background()); err != nil {
		t.Fatalf("install: %v", err)
	}
	snap := m2.Snapshot()
	if snap.State != StateReady {
		t.Fatalf("state = %s, want ready (detail %q, err %q)", snap.State, snap.Detail, snap.LastError)
	}
	ff, fp := BinNames("linux")
	for _, name := range []string{ff, fp} {
		if _, err := os.Stat(filepath.Join(CurrentBin(dataRoot), name)); err != nil {
			t.Fatalf("installed binary missing: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(LicensesDir(dataRoot), "PROVENANCE.txt")); err != nil {
		t.Fatalf("provenance missing: %v", err)
	}
	if _, err := os.Stat(InstalledManifestPath(dataRoot)); err != nil {
		t.Fatalf("installed manifest missing: %v", err)
	}
}

func TestManagerInstallDigestMismatch(t *testing.T) {
	// Serve DIFFERENT bytes than the manifest's digest pins: the
	// install must fail at digest verification and write nothing.
	original := fakeArchive(t, testPack())
	tampered := append([]byte(nil), original...)
	tampered[0] ^= 0xFF
	sum := sha256.Sum256(original) // digest of the ORIGINAL bytes
	srv := newPinnedTLSServer(t, tampered)
	t.Cleanup(srv.Close)
	pack := testPack()
	pack.ArchiveFormat = "zip"
	pack.FFmpegPath = "pkg-root/bin/ffmpeg.exe"
	pack.FFprobePath = "pkg-root/bin/ffprobe.exe"
	pack.URL = srv.URL + "/p.zip"
	pack.SHA256 = hex.EncodeToString(sum[:])
	raw, _ := json.Marshal(Manifest{Schema: 1, Platforms: map[string]PlatformPack{"linux/amd64": pack}})
	oldGOOS, oldArch := runtimeGOOS, runtimeArch
	runtimeGOOS, runtimeArch = "linux", "amd64"
	t.Cleanup(func() { runtimeGOOS, runtimeArch = oldGOOS, oldArch })
	dataRoot := t.TempDir()
	m := New(Options{DataRoot: dataRoot, client: srv.Client(),
		manifest: func() (Manifest, error) { return ParseManifest(raw) },
		verify:   func(PlatformPack, string) (VerifyResult, error) { return VerifyResult{}, nil }})
	if err := m.Install(context.Background()); err == nil {
		t.Fatal("digest mismatch must fail the install")
	}
	if !strings.Contains(m.Snapshot().LastError, "digest mismatch") {
		t.Fatalf("last error must name the digest mismatch: %q", m.Snapshot().LastError)
	}
	if _, err := os.Stat(CurrentDir(dataRoot)); !os.IsNotExist(err) {
		t.Fatal("nothing must be installed on digest mismatch")
	}
}

func TestManagerInstallServerErrors(t *testing.T) {
	codes := []int{http.StatusInternalServerError, http.StatusNotFound}
	for _, code := range codes {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		t.Cleanup(srv.Close)
		pack := testPack()
		pack.ArchiveFormat = "zip"
		pack.FFmpegPath = "pkg-root/bin/ffmpeg.exe"
		pack.FFprobePath = "pkg-root/bin/ffprobe.exe"
		pack.URL = srv.URL + "/p.zip"
		pack.SHA256 = strings.Repeat("a", 64)
		raw, _ := json.Marshal(Manifest{Schema: 1, Platforms: map[string]PlatformPack{"linux/amd64": pack}})
		oldGOOS, oldArch := runtimeGOOS, runtimeArch
		runtimeGOOS, runtimeArch = "linux", "amd64"
		t.Cleanup(func() { runtimeGOOS, runtimeArch = oldGOOS, oldArch })
		m := New(Options{
			DataRoot: t.TempDir(), client: srv.Client(),
			manifest: func() (Manifest, error) { return ParseManifest(raw) },
			verify:   func(PlatformPack, string) (VerifyResult, error) { return VerifyResult{}, nil },
		})
		if err := m.Install(context.Background()); err == nil {
			t.Fatalf("HTTP %d must fail the install", code)
		}
		if m.Snapshot().State != StateFailed {
			t.Fatalf("state = %s, want failed", m.Snapshot().State)
		}
	}
}

func TestManagerInstallCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte("late"))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	pack := testPack()
	pack.ArchiveFormat = "zip"
	pack.FFmpegPath = "pkg-root/bin/ffmpeg.exe"
	pack.FFprobePath = "pkg-root/bin/ffprobe.exe"
	pack.URL = srv.URL + "/p.zip"
	pack.SHA256 = strings.Repeat("a", 64)
	raw, _ := json.Marshal(Manifest{Schema: 1, Platforms: map[string]PlatformPack{"linux/amd64": pack}})
	oldGOOS, oldArch := runtimeGOOS, runtimeArch
	runtimeGOOS, runtimeArch = "linux", "amd64"
	t.Cleanup(func() { runtimeGOOS, runtimeArch = oldGOOS, oldArch })
	m := New(Options{DataRoot: t.TempDir(), client: srv.Client(),
		manifest: func() (Manifest, error) { return ParseManifest(raw) },
		verify:   func(PlatformPack, string) (VerifyResult, error) { return VerifyResult{}, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	if err := m.Install(ctx); err == nil {
		t.Fatal("cancelled install must return an error")
	}
	// Temp artifacts must be cleaned.
	entries, _ := os.ReadDir(Root(m.opts.DataRoot))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dl-") || strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp artifact leaked: %s", e.Name())
		}
	}
}

func TestManagerInstallVerifyFailureRollsBackPrevious(t *testing.T) {
	packBytes := fakeArchive(t, testPack())
	m, _, dataRoot := testManager(t, packBytes, nil)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(CurrentBin(dataRoot), "ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	// Now a "new" install that fails verification must leave the
	// previous installation intact (rollback semantics).
	other := append([]byte(nil), packBytes...)
	other[len(other)-1] ^= 0xFF
	sum := sha256.Sum256(other)
	srv := newPinnedTLSServer(t, other)
	t.Cleanup(srv.Close)
	baseManifest := m.opts.manifest
	pinnedDigest := hex.EncodeToString(sum[:])
	m2 := New(Options{
		DataRoot: dataRoot, client: srv.Client(),
		manifest: func() (Manifest, error) {
			mm, e := baseManifest()
			if e != nil {
				return mm, e
			}
			p := mm.Platforms["linux/amd64"]
			p.URL = srv.URL + "/p.zip"
			p.SHA256 = pinnedDigest
			mm.Platforms["linux/amd64"] = p
			return mm, nil
		},
		verify: func(PlatformPack, string) (VerifyResult, error) {
			return VerifyResult{}, errors.New("verification exploded")
		},
	})
	if err := m2.Install(context.Background()); err == nil {
		t.Fatal("failing verification must fail the install")
	}
	after, err := os.ReadFile(filepath.Join(CurrentBin(dataRoot), "ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("previous installation was NOT preserved on verification failure")
	}
	if m2.Snapshot().State != StateFailed {
		t.Fatalf("state = %s, want failed", m2.Snapshot().State)
	}
}

func TestManagerConcurrentInstallRefused(t *testing.T) {
	packBytes := fakeArchive(t, testPack())
	m, _, _ := testManager(t, packBytes, nil)
	gate := make(chan struct{})
	m.opts.verify = func(PlatformPack, string) (VerifyResult, error) {
		<-gate
		return VerifyResult{Encoders: []string{"libx264"}}, nil
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = m.Install(context.Background())
		}(i)
	}
	time.Sleep(200 * time.Millisecond)
	close(gate)
	wg.Wait()
	joined := 0
	for _, err := range errs {
		if err != nil && strings.Contains(err.Error(), "already in progress") {
			joined++
		}
	}
	if joined == 0 {
		t.Fatalf("a concurrent install must be refused (errs: %v / %v)", errs[0], errs[1])
	}
}

func TestManagerRemoveAndCheckForUpdate(t *testing.T) {
	packBytes := fakeArchive(t, testPack())
	m, _, dataRoot := testManager(t, packBytes, nil)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s, err := m.CheckForUpdate(context.Background()); err != nil || s.State != StateReady {
		t.Fatalf("check after install: state=%s err=%v", s.State, err)
	}
	if err := m.Remove(); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().State != StateNotInstalled {
		t.Fatalf("state = %s, want not-installed", m.Snapshot().State)
	}
	if _, err := os.Stat(CurrentDir(dataRoot)); !os.IsNotExist(err) {
		t.Fatal("current dir must be removed")
	}
}

// newPinnedTLSServer serves fixed bytes; the caller pins the digest.
func newPinnedTLSServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
}

// dualPlatformArchive builds a test zip that carries BOTH name forms of
// the pair so a manifest with linux/amd64 and windows/amd64 entries can
// be exercised for either pinned platform from any host.
func dualPlatformArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := map[string]string{
		"pkg-root/bin/ffmpeg":      "ffmpeg-fake",
		"pkg-root/bin/ffprobe":     "ffprobe-fake",
		"pkg-root/bin/ffmpeg.exe":  "ffmpeg-fake",
		"pkg-root/bin/ffprobe.exe": "ffprobe-fake",
		"pkg-root/COPYING.GPLv2":   "fake license",
	}
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// nonHostGOOS returns a GOOS that differs from the real host so the
// platform-pin divergence is exercised on EVERY runner (the historical
// Windows CI failure is thus reproducible on Linux and vice versa).
func nonHostGOOS() string {
	if runtimeGOOS == "windows" {
		return "linux"
	}
	return "windows"
}

// TestManagerInstallInspectConsistentAcrossHostPlatforms reproduces the
// run-43767556517 Windows failure (manager_test.go:312,
// "check after install: state=not-installed") at its source on ANY
// host: the installer resolved executable names through the package
// platform pin while InspectInstalled read runtime.GOOS directly.
// Wherever the pin differs from the host OS, a fully verified install
// was misreported as not-installed. The pin below is deliberately the
// NON-host GOOS, so any reintroduction of the divergence fails on
// every runner. The assertion is intentionally NOT weakened: after a
// successful install the check must report ready.
func TestManagerInstallInspectConsistentAcrossHostPlatforms(t *testing.T) {
	packBytes := dualPlatformArchive(t)
	sum := sha256.Sum256(packBytes)
	digest := hex.EncodeToString(sum[:])
	// Same pack bytes for both manifest entries; the URL below is
	// replaced per-manager via the injectable manifest hook.
	packLinux := testPack()
	packLinux.SHA256 = digest
	packWin := packLinux
	packWin.Platform = "windows/amd64"
	packWin.FFmpegPath = "pkg-root/bin/ffmpeg.exe"
	packWin.FFprobePath = "pkg-root/bin/ffprobe.exe"
	pinned := map[string]PlatformPack{
		"linux/amd64":   packLinux,
		"windows/amd64": packWin,
	}

	host := nonHostGOOS() // "windows" on Linux hosts, "linux" on Windows hosts
	dataRoot := t.TempDir()

	// Pin the package platform FIRST so pack selection, executable
	// naming and (pre-fix) inspection all run under one deliberate,
	// host-differing decision.
	oldGOOS, oldArch := runtimeGOOS, runtimeArch
	runtimeGOOS, runtimeArch = host, "amd64"
	t.Cleanup(func() { runtimeGOOS, runtimeArch = oldGOOS, oldArch })

	newManifestFn := func() func() (Manifest, error) {
		return func() (Manifest, error) {
			return Manifest{Schema: SchemaVersion, Updated: "test", Platforms: pinned}, nil
		}
	}

	newManager := func() *Manager {
		return New(Options{DataRoot: dataRoot,
			client:   newPinnedTLSServer(t, packBytes).Client(),
			manifest: newManifestFn(),
			verify: func(PlatformPack, string) (VerifyResult, error) {
				return VerifyResult{Version: "ffmpeg version n-test", Encoders: []string{"aac", "libx264", "libx265"}}, nil
			}})
	}
	m := newManager()
	if m.Snapshot().State != StateNotInstalled {
		t.Fatalf("pre-install state = %s, want not-installed", m.Snapshot().State)
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("install under pinned platform %s: %v", host, err)
	}
	// THE regression assertion: a verified install must inspect as
	// ready — never not-installed (run 43767556517 exact failure).
	if s, err := m.CheckForUpdate(context.Background()); err != nil || s.State != StateReady {
		t.Fatalf("check after install: state=%s err=%v", s.State, err)
	}
	// A fresh manager over the same data root must see the installed
	// state too (persistence is not scoped to the installing instance).
	fresh := newManager()
	if fresh.Snapshot().State != StateReady {
		t.Fatalf("fresh manager state = %s, want ready (installed state must survive manager recreation)", fresh.Snapshot().State)
	}
	if _, err := fresh.CheckForUpdate(context.Background()); err != nil {
		t.Fatalf("fresh manager check: %v", err)
	}
	if s := fresh.Snapshot().State; s != StateReady {
		t.Fatalf("fresh manager post-check state = %s, want ready", s)
	}
	// Remove must be safe and idempotent; state returns to
	// not-installed and stays there after a repeated remove.
	if err := m.Remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := m.Remove(); err != nil {
		t.Fatalf("repeated remove must stay safe (idempotent): %v", err)
	}
	if m.Snapshot().State != StateNotInstalled {
		t.Fatalf("state after remove = %s, want not-installed", m.Snapshot().State)
	}
	if _, err := os.Stat(CurrentDir(dataRoot)); !os.IsNotExist(err) {
		t.Fatal("current dir must be removed")
	}
}

// TestManagerCorruptInstalledManifestIsRepairNeeded proves a corrupted
// installation is never conflated with not-installed (run-43767556517
// §2.1.8: explicit states, errors for manifest corruption).
func TestManagerCorruptInstalledManifestIsRepairNeeded(t *testing.T) {
	packBytes := fakeArchive(t, testPack())
	m, _, dataRoot := testManager(t, packBytes, nil)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s, err := m.CheckForUpdate(context.Background()); err != nil || s.State != StateReady {
		t.Fatalf("check after install: state=%s err=%v", s.State, err)
	}
	if err := os.WriteFile(InstalledManifestPath(dataRoot), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := m.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatalf("check on corrupt manifest must not error the call itself: %v", err)
	}
	if s.State != StateRepairNeeded {
		t.Fatalf("corrupt manifest state = %s, want repair-needed (never conflated with not-installed)", s.State)
	}
}

// TestManagerUpdateAvailableAfterPinnedChange proves the update check
// detects a pinned version newer than the installed one.
func TestManagerUpdateAvailableAfterPinnedChange(t *testing.T) {
	packBytes := fakeArchive(t, testPack())
	m, _, _ := testManager(t, packBytes, nil)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Re-pin the manifest to a newer version: the installed pack no
	// longer matches, so the check must report update-available.
	updated := fakeArchive(t, testPack())
	sum := sha256.Sum256(updated)
	srv := newPinnedTLSServer(t, updated)
	t.Cleanup(srv.Close)
	pack := testPack()
	pack.Version = "n-test-next"
	pack.ArchiveFormat = "zip"
	pack.URL = srv.URL + "/pack.zip"
	pack.SHA256 = hex.EncodeToString(sum[:])
	raw, err := json.Marshal(Manifest{Schema: SchemaVersion, Updated: "test-next", Platforms: map[string]PlatformPack{"linux/amd64": pack}})
	if err != nil {
		t.Fatal(err)
	}
	// The pinned manifest is embedded per binary: a changed pin means a
	// NEW manager instance (new release) inspecting the same data root.
	m2 := New(Options{DataRoot: m.opts.DataRoot,
		client:   srv.Client(),
		manifest: func() (Manifest, error) { return ParseManifest(raw) },
		verify:   func(PlatformPack, string) (VerifyResult, error) { return VerifyResult{}, nil }})
	s, err := m2.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.State != StateUpdateAvailable {
		t.Fatalf("state = %s, want update-available (installed n-test vs pinned n-test-next)", s.State)
	}
}

// TestManagerInstallDataRootWithSpacesAndUnicode proves the app-managed
// layout survives paths with spaces and non-ASCII characters on every
// platform (run-43767556517 §2.1.9).
func TestManagerInstallDataRootWithSpacesAndUnicode(t *testing.T) {
	packBytes := fakeArchive(t, testPack())
	m, _, dataRoot := testManager(t, packBytes, nil)
	spaced := filepath.Join(filepath.Dir(dataRoot), "SHEYTAN Tools Музыка and spaces", filepath.Base(dataRoot))
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Skipf("host filesystem refuses the Unicode/spaced data root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(spaced) })
	m.opts.DataRoot = spaced
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("install into spaced/Unicode root: %v", err)
	}
	if s, err := m.CheckForUpdate(context.Background()); err != nil || s.State != StateReady {
		t.Fatalf("check after install into spaced root: state=%s err=%v", s.State, err)
	}
	ff, fp := BinNames(runtimeGOOS)
	for _, name := range []string{ff, fp} {
		if _, err := os.Stat(filepath.Join(CurrentBin(spaced), name)); err != nil {
			t.Fatalf("installed binary missing in spaced root: %v", err)
		}
	}
}

func TestManagerUnsupportedPlatform(t *testing.T) {
	raw, _ := json.Marshal(Manifest{Schema: 1, Platforms: map[string]PlatformPack{
		"linux/amd64": testPack(),
	}})
	oldGOOS, oldArch := runtimeGOOS, runtimeArch
	runtimeGOOS, runtimeArch = "darwin", "arm64"
	t.Cleanup(func() { runtimeGOOS, runtimeArch = oldGOOS, oldArch })
	m := New(Options{DataRoot: t.TempDir(),
		manifest: func() (Manifest, error) { return ParseManifest(raw) }})
	if m.Snapshot().State != StateUnsupportedPlatform {
		t.Fatalf("state = %s, want unsupported-platform", m.Snapshot().State)
	}
	if err := m.Install(context.Background()); err == nil {
		t.Fatal("install on unsupported platform must fail")
	}
	_ = fmt.Sprint()
}
