package gogui

import (
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
)

// TestMediaToolsDownloadTextWithTotal (run 45297707372): the live
// download line carries the transferred size, the total, the percent
// and the measured speed - every value from the snapshot, none
// invented.
func TestMediaToolsDownloadTextWithTotal(t *testing.T) {
	snap := mediatools.Snapshot{
		State:          mediatools.StateDownloading,
		Downloaded:     12 * 1024 * 1024,
		Total:          45 * 1024 * 1024,
		BytesPerSecond: 3.5 * 1024 * 1024,
	}
	got := mediaToolsDownloadText(snap)
	for _, want := range []string{"Downloading:", "12.0 MiB", "45.0 MiB", "27%", "/s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("download text missing %q: %s", want, got)
		}
	}
}

// TestMediaToolsDownloadTextUnknownTotal: when the server sends no
// content length the line says so instead of showing a fake percent.
func TestMediaToolsDownloadTextUnknownTotal(t *testing.T) {
	snap := mediatools.Snapshot{
		State:      mediatools.StateDownloading,
		Downloaded: 5 * 1024 * 1024,
	}
	got := mediaToolsDownloadText(snap)
	if !strings.Contains(got, "total size unknown") {
		t.Fatalf("unknown-total download text must say so: %s", got)
	}
	if strings.Contains(got, "%") {
		t.Fatalf("unknown-total download text must not show a percent: %s", got)
	}
	if !strings.Contains(got, "5.0 MiB") {
		t.Fatalf("transferred bytes missing: %s", got)
	}
}

// TestMediaToolsStatusShowsPackSize: the not-installed / update
// available / repair states surface the pinned pack download size so
// the user sees the size BEFORE choosing to download.
func TestMediaToolsStatusShowsPackSize(t *testing.T) {
	snap := mediatools.Snapshot{
		State: mediatools.StateUpdateAvailable,
		Pack: &mediatools.PlatformPack{
			Version: "7.1", Platform: "windows-amd64",
			ApproxDownloadMB: 42, ApproxInstalledMB: 96,
		},
	}
	got := mediaToolsStatusText(snap)
	if !strings.Contains(got, "~42 MiB compressed") || !strings.Contains(got, "~96 MiB installed") {
		t.Fatalf("status text missing the pack size: %s", got)
	}
}
