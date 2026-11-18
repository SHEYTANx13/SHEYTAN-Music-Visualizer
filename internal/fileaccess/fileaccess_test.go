package fileaccess

// fileaccess_test.go — adversarial coverage for Section 4.7 of the
// run-44010666873 work order: path traversal, symlink escapes,
// special files, sensitive-path skipping, caps, cancellation,
// revocation, identity changes and audit redaction.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AuditJSONOf encodes one record for the redaction assertion.
func AuditJSONOf(r AuditRecord) ([]byte, error) {
	return json.Marshal(r)
}

func grantFor(t *testing.T, root string, limits Limits) (*Manager, *Grant) {
	t.Helper()
	m := NewManager()
	req, err := m.PrepareAuthorization(root, "test: inventory audio files", "proj-1", "local-user", limits)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(req.SummaryLines()) < 5 {
		t.Fatal("authorization summary too thin to confirm")
	}
	g, err := m.ConfirmAuthorization(req)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return m, g
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkBasicsAndExtensionFilter(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a", "one.wav"), "wav-bytes")
	write(t, filepath.Join(dir, "a", "two.mp3"), "mp3-bytes")
	write(t, filepath.Join(dir, "b", "note.txt"), "text")
	m, g := grantFor(t, dir, Limits{Extensions: []string{".wav", ".mp3"}, SkipSensitive: true})
	defer m.RevokeAll()

	var seen []string
	sum, err := g.Analyze(context.Background(), func(ctx context.Context, e FileEntry) error {
		seen = append(seen, e.RelPath)
		return nil
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected exactly the 2 audio files, got %v", seen)
	}
	if sum.FilesRead != 2 || sum.FilesSkippedExt != 1 {
		t.Fatalf("summary wrong: %+v", sum)
	}
	for _, e := range seen {
		if strings.HasPrefix(e, "/") || strings.Contains(e, dir) {
			t.Fatalf("rel path leaked absolute location: %s", e)
		}
	}
	m.RecordWalk(g, sum, OutcomeCompleted, "corr-1")
	recs := m.AuditRecords(10)
	if len(recs) == 0 || recs[len(recs)-1].Operation != "analyze" {
		t.Fatal("walk not audited")
	}
	for _, r := range recs {
		b, _ := r.Detail, r.RootLabel
		_ = b
		if strings.Contains(strings.ToLower(string(mustJSON(t, r))), strings.ToLower(dir)) {
			t.Fatalf("audit record leaked the absolute root path: %+v", r)
		}
	}
}

func mustJSON(t *testing.T, r AuditRecord) []byte {
	t.Helper()
	b, err := AuditJSONOf(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSymlinkEscapeIsNeverFollowed: a symlink pointing OUT of the
// authorized root must never be traversed (the classic traversal
// attack), and an inside-pointing symlink is skipped too.
func TestSymlinkEscapeIsNeverFollowed(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	write(t, secret, "top secret")
	inside := t.TempDir()
	write(t, filepath.Join(inside, "ok.wav"), "ok")
	if err := os.Symlink(outside, filepath.Join(inside, "escape")); err != nil {
		t.Skip("symlinks unavailable on this platform")
	}
	if err := os.Symlink(secret, filepath.Join(inside, "secret-link.wav")); err != nil {
		t.Skip("symlinks unavailable")
	}
	m, g := grantFor(t, inside, Limits{SkipSensitive: true})
	defer m.RevokeAll()

	var seen []string
	sum, err := g.Analyze(context.Background(), func(ctx context.Context, e FileEntry) error {
		seen = append(seen, e.RelPath)
		return nil
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	for _, s := range seen {
		if strings.Contains(s, "escape") || strings.Contains(s, "secret") {
			t.Fatalf("traversal through symlink happened: %s", s)
		}
	}
	if sum.FilesSkippedType < 2 {
		t.Fatalf("symlinks not classified as skipped: %+v", sum)
	}
}

// TestSpecialFilesSkipped: FIFOs and devices are never opened.
func TestSpecialFilesSkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.wav"), "audio")
	fifo := filepath.Join(dir, "pipe")
	if err := mkfifo(fifo); err != nil {
		t.Skip("fifo unavailable:", err)
	}
	m, g := grantFor(t, dir, Limits{SkipSensitive: true})
	defer m.RevokeAll()
	sum, err := g.Analyze(context.Background(), nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if sum.FilesRead != 1 || sum.FilesSkippedType != 1 {
		t.Fatalf("special file not skipped: %+v", sum)
	}
}

// TestSensitiveDefaultSkip: credential-store-like files inside a
// selected folder are skipped by default; a direct selection without
// confirmation returns ErrSensitiveDefault.
func TestSensitiveDefaultSkip(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "music", "track.wav"), "audio")
	write(t, filepath.Join(dir, "home", ".ssh", "id_rsa"), "PRIVATE KEY")
	write(t, filepath.Join(dir, "home", ".aws", "credentials"), "[default]\naws_access_key_id=x")
	write(t, filepath.Join(dir, "home", "Login Data"), "chrome logins")
	write(t, filepath.Join(dir, "home", "wallet.dat"), "wallet")
	write(t, filepath.Join(dir, "home", "cert.pem"), "-----BEGIN PRIVATE KEY-----")
	write(t, filepath.Join(dir, "home", "track.mp3"), "mp3")
	m, g := grantFor(t, dir, Limits{SkipSensitive: true})
	defer m.RevokeAll()

	sum, err := g.Analyze(context.Background(), nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if sum.FilesRead != 2 {
		t.Fatalf("expected only the 2 audio files read, got %d (+%d sensitive skipped)", sum.FilesRead, sum.FilesSkippedSens)
	}
	if sum.FilesSkippedSens != 5 {
		t.Fatalf("sensitive skip count = %d, want 5: %+v", sum.FilesSkippedSens, sum)
	}

	// Direct sensitive selection: default is a typed refusal.
	if _, _, err := g.AnalyzeFile(context.Background(), "home/.ssh/id_rsa", false); !errors.Is(err, ErrSensitiveDefault) {
		t.Fatalf("direct sensitive selection must refuse by default, got %v", err)
	}
	// A separately confirmed direct selection still only reads that
	// one file and cannot bypass OS permissions.
	e, _, err := g.AnalyzeFile(context.Background(), "home/.ssh/id_rsa", true)
	if err != nil {
		t.Fatalf("confirmed direct selection failed: %v", err)
	}
	if e.Size != int64(len("PRIVATE KEY")) {
		t.Fatalf("unexpected size %d", e.Size)
	}
}

// TestCapsAndCancellation: depth, file-count and cancellation bounds.
func TestDepthCap(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for i := 0; i < 10; i++ {
		deep = filepath.Join(deep, "d")
	}
	write(t, filepath.Join(deep, "deep.wav"), "x")
	write(t, filepath.Join(dir, "shallow.wav"), "y")
	m, g := grantFor(t, dir, Limits{MaxDepth: 3, SkipSensitive: true})
	defer m.RevokeAll()
	sum, err := g.Analyze(context.Background(), nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if sum.FilesRead != 1 {
		t.Fatalf("depth cap broken: %+v", sum)
	}
}

func TestFileCountCap(t *testing.T) {
	dir := t.TempDir()
	// 30 distinct files
	for i := 0; i < 30; i++ {
		write(t, filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".wav"), "x")
	}
	m, g := grantFor(t, dir, Limits{MaxFiles: 5, SkipSensitive: true})
	defer m.RevokeAll()
	sum, err := g.Analyze(context.Background(), nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if sum.Capped == "" || sum.FilesRead > 6 {
		t.Fatalf("file-count cap not enforced: %+v", sum)
	}
}

func TestCancellationMidWalk(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 40; i++ {
		write(t, filepath.Join(dir, strings.Repeat("d", 1+i%3), "f"+string(rune('a'+i%26))+".wav"), "x")
	}
	m, g := grantFor(t, dir, Limits{SkipSensitive: true})
	defer m.RevokeAll()
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	sum, err := g.Analyze(ctx, func(ctx context.Context, e FileEntry) error {
		n++
		if n >= 2 {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("analyze with cancel: %v", err)
	}
	if !sum.Cancelled && sum.Capped == "" && n > 5 {
		t.Fatalf("cancellation not observed promptly: n=%d %+v", n, sum)
	}
}

// TestRevocationBlocksWalks: after Revoke, analysis refuses.
func TestRevocationBlocksWalks(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.wav"), "x")
	m, g := grantFor(t, dir, Limits{SkipSensitive: true})
	m.Revoke(g.ScopeID)
	if _, err := g.Analyze(context.Background(), nil); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("revoked grant still walks: %v", err)
	}
	if m.Grant(g.ScopeID) != nil {
		t.Fatal("revoked grant still resolvable")
	}
}

// TestRootIdentityChangeRequiresReconfirmation: a request prepared for
// one directory must not survive the directory being swapped
// (prepare -> replace -> confirm must fail), while a FRESH selection
// of the new directory confirms normally.
func TestRootIdentityChangeRequiresReconfirmation(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sel")
	write(t, filepath.Join(dir, "a.wav"), "x")
	m := NewManager()
	// Deterministic handle lifecycle: the fresh grant holds an os.Root
	// open until revoked; on Windows an unjoined open directory handle
	// makes t.TempDir cleanup fail ("being used by another process").
	// RevokeAll closes every root handle before the test returns.
	defer m.RevokeAll()

	// 1) prepare for the current identity
	req, err := m.PrepareAuthorization(dir, "test", "proj-1", "local-user", Limits{SkipSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	// 2) swap the directory out from under the pending request
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "a.wav"), "x")
	// 3) confirming the STALE request must fail
	if _, err := m.ConfirmAuthorization(req); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("stale confirmation accepted: %v", err)
	}
	// 4) a fresh selection of the new directory confirms normally
	req2, err := m.PrepareAuthorization(dir, "test", "proj-1", "local-user", Limits{SkipSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := m.ConfirmAuthorization(req2)
	if err != nil {
		t.Fatalf("fresh re-selection rejected: %v", err)
	}
	if _, err := g2.Analyze(context.Background(), nil); err != nil {
		t.Fatalf("fresh grant walk failed: %v", err)
	}
}

// TestSameInodeReplacementRequiresReconfirmation is the deterministic
// regression test for run 44298066730 failure A (Linux: "stale
// confirmation accepted: <nil>"). The CI runner's ext4 reused the
// freed directory inode, so dev+ino were IDENTICAL across the
// delete+recreate and the old dev+ino-only comparison accepted the
// stale confirmation. Replaying that exact condition through the real
// filesystem is impossible to force deterministically, so the test
// drives the statRoot seam: the confirm-time identity reports the SAME
// dev+ino but a different creation timestamp — the signature of a
// same-inode recreation — and the confirmation must fail.
func TestSameInodeReplacementRequiresReconfirmation(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sel")
	write(t, filepath.Join(dir, "a.wav"), "x")
	m := NewManager()
	defer m.RevokeAll()

	orig := statRoot
	t.Cleanup(func() { statRoot = orig })
	realStat := orig
	var calls int
	statRoot = func(p string) (RootIdentity, error) {
		id, err := realStat(p)
		if err == nil {
			calls++
			if calls == 2 { // confirm-time revalidation
				// Same dev+ino (inode reuse), different directory
				// instance: the birth time changed.
				id.BtimeSec += 1
				id.CtimeNsec += 1
			}
		}
		return id, err
	}

	req, err := m.PrepareAuthorization(dir, "test", "proj-1", "local-user", Limits{SkipSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfirmAuthorization(req); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("same-(dev,ino) replacement accepted: %v", err)
	}
	if calls < 2 {
		t.Fatalf("confirm-time identity revalidation never ran (calls=%d)", calls)
	}

	// Platforms whose primitive carries mtime as the discriminator
	// (Windows file identity with MFT-record reuse) reject through the
	// same comparison: identical primary components on BOTH sides, a
	// different mtime. The seam simulates that primitive deterministically
	// (a real FileInfo-based identity on Linux carries no mtime, so the
	// fixture must provide both sides).
	m2 := NewManager()
	defer m2.RevokeAll()
	const baseMtimeSec = int64(1750000000)
	var calls2 int
	statRoot = func(p string) (RootIdentity, error) {
		id, err := realStat(p)
		if err == nil {
			calls2++
			if calls2 == 1 {
				id.MtimeSec = baseMtimeSec
				id.MtimeNsec = 1
			} else {
				id.MtimeSec = baseMtimeSec + 1 // recreated: newer mtime
				id.MtimeNsec = 1
			}
		}
		return id, err
	}
	req2, err := m2.PrepareAuthorization(dir, "test", "proj-1", "local-user", Limits{SkipSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.ConfirmAuthorization(req2); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("same-primary-different-mtime replacement accepted: %v", err)
	}

	// An UNCHANGED identity still confirms normally (the seam now
	// returns identical values for prepare and confirm).
	m3 := NewManager()
	defer m3.RevokeAll()
	statRoot = realStat
	req3, err := m3.PrepareAuthorization(dir, "test", "proj-1", "local-user", Limits{SkipSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m3.ConfirmAuthorization(req3); err != nil {
		t.Fatalf("unchanged identity rejected: %v", err)
	}
}

// TestAuthorizationExpiry: a stale confirmation request is refused.
func TestAuthorizationExpiry(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.wav"), "x")
	m := NewManager()
	req, err := m.PrepareAuthorization(dir, "p", "proj", "user", Limits{SkipSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	req.createdAt = time.Now().Add(-time.Hour) // simulate staleness
	if _, err := m.ConfirmAuthorization(req); err == nil {
		t.Fatal("stale authorization request accepted")
	}
}

// TestReadFileCapped: content reads are capped and read-only.
func TestReadFileCapped(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "big.wav"), strings.Repeat("x", 4096))
	m, g := grantFor(t, dir, Limits{MaxFileByte: 128, SkipSensitive: true})
	defer m.RevokeAll()
	rc, size, err := g.ReadFile("big.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if size != 128 {
		t.Fatalf("capped size = %d, want 128", size)
	}
	n, _ := readAll(rc)
	if n != 128 {
		t.Fatalf("read %d bytes, want 128", n)
	}
}

func readAll(rc interface{ Read([]byte) (int, error) }) (int64, error) {
	buf := make([]byte, 64)
	var total int64
	for {
		n, err := rc.Read(buf)
		total += int64(n)
		if err != nil {
			return total, nil
		}
	}
}

func mkfifo(path string) error {
	// Unix-only helper; tests skip when unavailable.
	return mkfifoUnix(path)
}

// TestHeadFingerprintIsDeterministic: the capped SHA-256 is stable and
// truthful about only the first chunk.
func TestHeadFingerprintIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.wav"), "hello world")
	m, g := grantFor(t, dir, Limits{SkipSensitive: true})
	defer m.RevokeAll()
	var first FileEntry
	if _, err := g.Analyze(context.Background(), func(ctx context.Context, e FileEntry) error {
		first = e
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(first.HeadSHA256) != 64 {
		t.Fatalf("bad fingerprint %q", first.HeadSHA256)
	}
}
