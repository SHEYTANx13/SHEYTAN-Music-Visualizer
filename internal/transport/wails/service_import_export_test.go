package wails

// service_import_export_test.go — fixtures for the import.audio /
// export.start / export.cancel dispatch and the Song DNA / export
// status surfaces (repair/run-46033044814).
//
// The fixtures pin the honest behaviors:
//   - without a native dialog host: typed rejected error, exact reason;
//   - with a stub host: dialog errors propagate, user cancels are
//     success-with-no-change, bad arguments are rejected EARLY;
//   - /api/songdna and /api/export report honest states for an empty
//     studio (no-track / idle), never synthesized data.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportAudioWithoutDialogHostIsRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	err := svc.RunCommand("import.audio", "")
	if err == nil {
		t.Fatal("import.audio without a dialog host must be refused, got nil")
	}
	fe, ok := err.(*FacadeError)
	if !ok {
		t.Fatalf("error is %T, want *FacadeError: %v", err, err)
	}
	if fe.Kind != KindRejected || fe.Command != "import.audio" {
		t.Errorf("error = %+v, want kind %q command %q", fe, KindRejected, "import.audio")
	}
	if !strings.Contains(fe.Reason, "no native dialog host registered") {
		t.Errorf("reason = %q, want the exact headless-context reason", fe.Reason)
	}
}

func TestImportAudioDialogPaths(t *testing.T) {
	svc, _, _ := newTestService(t)

	// Dialog error propagates as a typed rejected error.
	svc.setDialogs(HostDialogsFuncs{
		OpenAudio: func() (string, error) { return "", errors.New("gtk backend exploded") },
	})
	err := svc.RunCommand("import.audio", "")
	if err == nil || !strings.Contains(err.Error(), "import dialog failed: gtk backend exploded") {
		t.Errorf("dialog error = %v, want wrapped 'import dialog failed' reason", err)
	}

	// User cancel ("", nil) is success-with-no-change.
	svc.setDialogs(HostDialogsFuncs{
		OpenAudio: func() (string, error) { return "", nil },
	})
	if err := svc.RunCommand("import.audio", ""); err != nil {
		t.Errorf("user cancel must be nil error, got %v", err)
	}
}

func TestExportStartValidationBeforeDialog(t *testing.T) {
	svc, _, _ := newTestService(t)
	// Every case must fail on argument validation WITHOUT touching a
	// dialog host (none is registered here - a dialog call would fail
	// with the host error, which would prove validation never ran).
	cases := []struct {
		args string
		want string
	}{
		{`{"width":0}`, "resolution"},
		{`{"width":9999,"height":1080}`, "resolution"},
		{`{"fps":200}`, "fps"},
		{`{"videoCodec":"av1"}`, "videoCodec"},
		{`{"qualityPreset":"lossless"}`, "qualityPreset"},
	}
	for _, c := range cases {
		err := svc.RunCommand("export.start", c.args)
		if err == nil {
			t.Errorf("export.start %s must be refused", c.args)
			continue
		}
		fe, ok := err.(*FacadeError)
		if !ok || fe.Kind != KindInvalidArgs {
			t.Errorf("export.start %s error = %v, want invalid-args", c.args, err)
			continue
		}
		if !strings.Contains(fe.Reason, c.want) {
			t.Errorf("export.start %s reason = %q, want substring %q", c.args, fe.Reason, c.want)
		}
	}
}

func TestExportStartWithoutHostOrTrackIsRejected(t *testing.T) {
	svc, _, _ := newTestService(t)

	err := svc.RunCommand("export.start", "")
	if err == nil || !strings.Contains(err.Error(), "no native dialog host registered") {
		t.Errorf("export.start without host = %v, want host-missing rejection", err)
	}

	// With a host but no track: the studio refuses before any dialog.
	svc.setDialogs(HostDialogsFuncs{
		SaveExport: func(defaultName string) (string, error) {
			t.Errorf("save dialog must not open without a track (defaultName %q)", defaultName)
			return "", nil
		},
	})
	err = svc.RunCommand("export.start", "")
	if err == nil || !strings.Contains(err.Error(), "no track loaded") {
		t.Errorf("export.start without track = %v, want 'no track loaded'", err)
	}
}

func TestExportCancelWithoutJobIsRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	err := svc.RunCommand("export.cancel", "")
	if err == nil {
		t.Fatal("export.cancel with no job must be refused")
	}
	fe, ok := err.(*FacadeError)
	if !ok || fe.Kind != KindRejected {
		t.Fatalf("error = %v, want rejected FacadeError", err)
	}
	if !strings.Contains(fe.Reason, "no running export") {
		t.Errorf("reason = %q, want the no-running-export reason", fe.Reason)
	}
}

func TestSongDNAAndExportStatusHonestEmptyStates(t *testing.T) {
	svc, _, _ := newTestService(t)

	dna := svc.GetSongDNA()
	if dna["state"] != "no-track" {
		t.Errorf("songdna state = %v, want no-track", dna["state"])
	}
	note, _ := dna["note"].(string)
	if note == "" {
		t.Error("no-track state must carry an actionable note")
	}

	status := svc.GetExportStatus()
	if status["state"] != "idle" {
		t.Errorf("export state = %v, want idle", status["state"])
	}
}

func TestSongDNAAndExportRoutesServeJSON(t *testing.T) {
	svc, _, _ := newTestService(t)
	handler := NewAssetHandler(svc)

	for _, path := range []string{"/api/songdna", "/api/export"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Errorf("GET %s body is not JSON: %v", path, err)
		}
		if doc["state"] == nil {
			t.Errorf("GET %s body lacks the honest state field: %s", path, rec.Body.String())
		}
	}
}

func TestExportDefaultName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"My Song.mp3", "My-Song.mp4"},
		{"../../etc/passwd", "etc-passwd.mp4"},
		{"", "sheytan-music-video.mp4"},
		{"   ", "sheytan-music-video.mp4"},
		{"track — müët mix", "track-mt-mix.mp4"},
		{"video.mp4", "video.mp4"},
	}
	for _, c := range cases {
		got := exportDefaultName(c.in)
		if got != c.want {
			t.Errorf("exportDefaultName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
