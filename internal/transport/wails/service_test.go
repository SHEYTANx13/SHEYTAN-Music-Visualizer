package wails

// service_test.go — display-less verification of the web shell facade:
// the settings state machine, RunCommand validation and dispatch, the
// diagnostics shape, the byte-identity of the embedded tokens copy and
// the /api/* mux over net/http/httptest (acceptance level for the
// facade; the webview itself cannot run on a display-less machine).
//
// The tests run against a REAL studio instance (studio.New) exactly
// like internal/studio tests do: SHEYTAN_DATA_DIR points at a
// per-test temp directory so the user config location is never
// touched.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/frontend/wails/dist"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
)

// allCommandIDs is the complete canonical command set (kept in lockstep
// with internal/presentation/commands.go; the presentation package test
// owns the authority, this slice pins its own exhaustive coverage).
var allCommandIDs = []presentation.CommandID{
	presentation.CmdImportAudio,
	presentation.CmdPlayPause,
	presentation.CmdStop,
	presentation.CmdSeekBack,
	presentation.CmdSeekForward,
	presentation.CmdSetVolume,
	presentation.CmdProjectSave,
	presentation.CmdProjectOpen,
	presentation.CmdExportStart,
	presentation.CmdExportCancel,
	presentation.CmdRendererSelect,
	presentation.CmdModeSet,
	presentation.CmdPaletteSet,
	presentation.CmdSensitivitySet,
	presentation.CmdBackgroundLayerSet,
	presentation.CmdPresetApply,
	presentation.CmdIntensitySet,
	presentation.CmdReducedMotionSet,
	presentation.CmdReducedGlowSet,
	presentation.CmdNavigate,
	presentation.CmdTabsReorder,
	presentation.CmdTabsReset,
	presentation.CmdSplitOrientation,
	presentation.CmdPanelCollapse,
	presentation.CmdPanelRestore,
	presentation.CmdLayoutReset,
	presentation.CmdMediaToolsInstall,
	presentation.CmdMediaToolsOpen,
	presentation.CmdAnalyzePath,
	presentation.CmdHelpOpen,
	presentation.CmdLogsReveal,
	presentation.CmdCopyToClipboard,
	presentation.CmdQuit,
	presentation.CmdAbout,
}

// newTestService builds a real studio + facade for one test.
// SHEYTAN_DATA_DIR points at a fresh temp dir (env-override mode),
// mirroring the internal/studio test setup. The resolved data dir is
// returned for exact-root assertions.
func newTestService(t *testing.T) (*SheytanService, *studio.Studio, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("SHEYTAN_DATA_DIR", dataDir)
	st, err := studio.New()
	if err != nil {
		t.Fatalf("studio.New: %v", err)
	}
	t.Cleanup(st.Close)
	return NewService(st), st, dataDir
}

func mustBeFacadeError(t *testing.T, err error, kind string) *FacadeError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s FacadeError, got nil", kind)
	}
	var fe *FacadeError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FacadeError, got %T: %v", err, err)
	}
	if fe.Kind != kind {
		t.Fatalf("FacadeError kind = %q, want %q (reason: %s)", fe.Kind, kind, fe.Reason)
	}
	return fe
}

func TestSettingsStateMachine(t *testing.T) {
	svc, _, _ := newTestService(t)

	doc := design.MustLoad()

	initial := svc.GetSettings()
	if initial.Intensity != doc.DefaultIntensity {
		t.Errorf("initial intensity = %q, want default %q", initial.Intensity, doc.DefaultIntensity)
	}
	if initial.ReducedMotion || initial.ReducedGlow {
		t.Errorf("initial accessibility flags should be off, got %+v", initial)
	}

	// Valid intensity is accepted and stored.
	if err := svc.SetIntensity("subtle"); err != nil {
		t.Fatalf("SetIntensity(subtle): %v", err)
	}
	if got := svc.GetSettings().Intensity; got != "subtle" {
		t.Errorf("intensity = %q, want subtle", got)
	}

	// Case/whitespace normalization is part of the validation contract.
	if err := svc.SetIntensity("  RITUAL "); err != nil {
		t.Fatalf("SetIntensity with casing: %v", err)
	}
	if got := svc.GetSettings().Intensity; got != "ritual" {
		t.Errorf("intensity = %q, want ritual", got)
	}

	// Invalid intensity is REJECTED, and the previous value survives.
	err := svc.SetIntensity("inferno")
	mustBeFacadeError(t, err, KindInvalidArgs)
	if got := svc.GetSettings().Intensity; got != "ritual" {
		t.Errorf("intensity changed after rejection: %q", got)
	}
	if !strings.Contains(err.Error(), "unknown intensity") {
		t.Errorf("rejection should name the problem, got: %v", err)
	}

	// Accessibility switches round-trip.
	svc.SetReducedMotion(true)
	svc.SetReducedGlow(true)
	got := svc.GetSettings()
	if !got.ReducedMotion || !got.ReducedGlow {
		t.Errorf("accessibility switches not stored: %+v", got)
	}
	svc.SetReducedMotion(false)
	svc.SetReducedGlow(false)
	got = svc.GetSettings()
	if got.ReducedMotion || got.ReducedGlow {
		t.Errorf("accessibility switches not cleared: %+v", got)
	}
}

func TestRunCommandValidationAndDispatch(t *testing.T) {
	svc, _, _ := newTestService(t)

	// Unknown ID is an explicit typed error.
	err := svc.RunCommand("totally.bogus", "")
	fe := mustBeFacadeError(t, err, KindUnknownCommand)
	if fe.Command != "totally.bogus" {
		t.Errorf("error command = %q, want totally.bogus", fe.Command)
	}

	// Known-but-unsupported commands carry the boundary reason.
	for _, id := range []presentation.CommandID{
		presentation.CmdImportAudio,
		presentation.CmdProjectSave,
		presentation.CmdExportStart,
		presentation.CmdAnalyzePath,
		presentation.CmdMediaToolsInstall,
	} {
		err := svc.RunCommand(string(id), "")
		fe := mustBeFacadeError(t, err, KindUnsupportedCommand)
		if fe.Reason == "" {
			t.Errorf("command %s must carry an explicit reason", id)
		}
	}

	// Valid dispatch: volume with validation.
	if err := svc.RunCommand(string(presentation.CmdSetVolume), `{"volume":0.42}`); err != nil {
		t.Fatalf("set-volume: %v", err)
	}
	if got := svc.GetTransportSnapshot().Volume; got != 0.42 {
		t.Errorf("volume = %g, want 0.42", got)
	}
	for _, bad := range []string{`{}`, `{"volume":1.5}`, `{"volume":-0.1}`, `not json`, `5`} {
		if err := svc.RunCommand(string(presentation.CmdSetVolume), bad); err == nil {
			t.Errorf("set-volume(%q) should fail", bad)
		} else {
			mustBeFacadeError(t, err, KindInvalidArgs)
		}
	}

	// Valid dispatch: theme settings through the command path.
	if err := svc.RunCommand(string(presentation.CmdIntensitySet), `{"name":"infernal"}`); err != nil {
		t.Fatalf("intensity-set: %v", err)
	}
	if got := svc.GetSettings().Intensity; got != "infernal" {
		t.Errorf("intensity = %q, want infernal", got)
	}
	if err := svc.RunCommand(string(presentation.CmdReducedMotionSet), `{"enabled":true}`); err != nil {
		t.Fatalf("reduced-motion-set: %v", err)
	}
	if err := svc.RunCommand(string(presentation.CmdReducedGlowSet), `{"enabled":true}`); err != nil {
		t.Fatalf("reduced-glow-set: %v", err)
	}
	got := svc.GetSettings()
	if !got.ReducedMotion || !got.ReducedGlow {
		t.Errorf("accessibility commands not applied: %+v", got)
	}
	if err := svc.RunCommand(string(presentation.CmdIntensitySet), `{"name":"nope"}`); err == nil {
		t.Error("intensity-set with unknown name should fail")
	}
	if err := svc.RunCommand(string(presentation.CmdReducedMotionSet), `{}`); err == nil {
		t.Error("reduced-motion-set without enabled should fail")
	}

	// Preset apply against the real built-in preset set.
	if err := svc.RunCommand(string(presentation.CmdPresetApply), `{"name":"Hellfire Pulse"}`); err != nil {
		t.Fatalf("preset-apply: %v", err)
	}
	err = svc.RunCommand(string(presentation.CmdPresetApply), `{"name":"Does Not Exist"}`)
	fe = mustBeFacadeError(t, err, KindInvalidArgs)
	if !strings.Contains(fe.Reason, "Hellfire Pulse") {
		t.Errorf("preset rejection should list available presets, got: %s", fe.Reason)
	}

	// Honest core rejections: playing with no track is refused with the
	// actionable studio message, quitting without a window host is
	// refused explicitly (never a fake success).
	err = svc.RunCommand(string(presentation.CmdPlayPause), "")
	fe = mustBeFacadeError(t, err, KindRejected)
	if !strings.Contains(fe.Reason, "no track loaded") {
		t.Errorf("no-track rejection should carry the studio guidance, got: %s", fe.Reason)
	}
	err = svc.RunCommand(string(presentation.CmdQuit), "")
	fe = mustBeFacadeError(t, err, KindRejected)
	if !strings.Contains(fe.Reason, "no window host") {
		t.Errorf("hostless quit should be refused explicitly, got: %s", fe.Reason)
	}

	// Seek and stop are valid with no transport (studio: no-op, honest
	// idle state, not an error).
	if err := svc.RunCommand(string(presentation.CmdSeekBack), ""); err != nil {
		t.Errorf("seek-back without track: %v", err)
	}
	if err := svc.RunCommand(string(presentation.CmdSeekForward), `{"seconds":30}`); err != nil {
		t.Errorf("seek-forward without track: %v", err)
	}
	if err := svc.RunCommand(string(presentation.CmdStop), ""); err != nil {
		t.Errorf("stop without track: %v", err)
	}
	if err := svc.RunCommand(string(presentation.CmdSeekBack), `{"seconds":0}`); err == nil {
		t.Error("seek delta 0 should be rejected")
	}
	if err := svc.RunCommand(string(presentation.CmdSeekBack), `{"seconds":-5}`); err == nil {
		t.Error("negative seek delta should be rejected")
	}
}

// TestCommandCatalogIsExhaustive pins the dispatch/unsupported split:
// every canonical command ID must be exactly one of them, and no
// dispatch may fall into the "facade bug" default branch.
func TestCommandCatalogIsExhaustive(t *testing.T) {
	svc, _, _ := newTestService(t)

	dispatched := map[presentation.CommandID]bool{}
	for _, id := range dispatchedCommands {
		dispatched[id] = true
	}
	unsupported := map[presentation.CommandID]bool{}
	for id := range unsupportedReasons {
		unsupported[id] = true
	}

	for _, id := range allCommandIDs {
		if err := presentation.ValidateCommand(id); err != nil {
			t.Fatalf("canonical id %q failed validation: %v", id, err)
		}
		d, u := dispatched[id], unsupported[id]
		if d == u {
			t.Errorf("command %q must be exactly one of dispatched/unsupported (got d=%t u=%t)", id, d, u)
		}
		// No dispatch may reach the default "facade bug" branch. Empty
		// args exercise the strictest path of every supported command.
		err := svc.RunCommand(string(id), "")
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "facade bug") {
			t.Errorf("command %q fell into the facade-bug default branch: %v", id, err)
		}
	}

	// The catalog endpoint reports the same split, sorted.
	catalog := svc.GetCommands()
	if len(catalog.Supported) != len(dispatchedCommands) {
		t.Errorf("catalog supported = %d, want %d", len(catalog.Supported), len(dispatchedCommands))
	}
	if len(catalog.Unsupported) != len(unsupportedReasons) {
		t.Errorf("catalog unsupported = %d, want %d", len(catalog.Unsupported), len(unsupportedReasons))
	}
	for i := 1; i < len(catalog.Supported); i++ {
		if catalog.Supported[i-1].ID >= catalog.Supported[i].ID {
			t.Errorf("catalog supported not sorted at %d", i)
		}
	}
	for i := 1; i < len(catalog.Unsupported); i++ {
		if catalog.Unsupported[i-1].ID >= catalog.Unsupported[i].ID {
			t.Errorf("catalog unsupported not sorted at %d", i)
		}
	}
}

func TestGetDiagnosticsShape(t *testing.T) {
	svc, _, dataDir := newTestService(t)

	d := svc.GetDiagnostics()

	if d.Build.Version == "" || d.Build.GoVersion == "" {
		t.Errorf("build identity incomplete: %+v", d.Build)
	}
	if d.DataMode != string(apppaths.ModeEnvOverride) {
		t.Errorf("data mode = %q, want %q", d.DataMode, apppaths.ModeEnvOverride)
	}
	if d.DataRoot != dataDir {
		t.Errorf("data root = %q, want %q", d.DataRoot, dataDir)
	}
	if d.LogPath == "" || !strings.HasSuffix(d.LogPath, filepath.Join("logs", "app.log")) {
		t.Errorf("log path = %q, want <data root>/logs/app.log", d.LogPath)
	}
	switch render.Backend(d.Renderer.Backend) {
	case render.BackendCPU, render.BackendGPU:
		// honest evidence
	default:
		t.Errorf("renderer backend = %q, want cpu|gpu", d.Renderer.Backend)
	}

	if d.CheckedAt.IsZero() || time.Since(d.CheckedAt) > time.Minute {
		t.Errorf("checkedAt not recent: %v", d.CheckedAt)
	}

	wantChecks := []string{"design-tokens", "data-layout", "log-directory", "renderer-evidence", "ffmpeg-env", "studio-core"}
	seen := map[string]bool{}
	for _, c := range d.SelfChecks {
		if c.Name == "" {
			t.Error("self check with empty name")
		}
		seen[c.Name] = true
	}
	for _, name := range wantChecks {
		if !seen[name] {
			t.Errorf("missing self check %q in %+v", name, d.SelfChecks)
		}
	}
	for _, c := range d.SelfChecks {
		if !c.Passed {
			t.Errorf("self check %q failed on a healthy facade: %s", c.Name, c.Detail)
		}
	}
}

// TestGetTokensMatchesDesignRaw pins two byte-identity contracts:
// the facade serves design.Raw() verbatim, and the embedded dist copy
// of tokens.json can never drift from the canonical document.
func TestGetTokensMatchesDesignRaw(t *testing.T) {
	svc, _, _ := newTestService(t)

	served, err := svc.GetTokens()
	if err != nil {
		t.Fatalf("GetTokens: %v", err)
	}
	if !bytes.Equal([]byte(served), design.Raw()) {
		t.Error("GetTokens bytes differ from design.Raw()")
	}

	embedded, err := io.ReadAll(mustOpenDist(t, "tokens.json"))
	if err != nil {
		t.Fatalf("read embedded tokens.json: %v", err)
	}
	if !bytes.Equal(embedded, design.Raw()) {
		t.Error("frontend/wails/dist/tokens.json drifted from design/tokens.json — regenerate the copy")
	}

	// The embedded asset set is exactly the four served files.
	handled := dist.HandledAssets()
	if len(handled) != 4 {
		t.Fatalf("HandledAssets = %v, want the four embedded files", handled)
	}
	for _, name := range handled {
		rc, err := dist.FS().Open(name)
		if err != nil {
			t.Errorf("embedded asset %s missing: %v", name, err)
			continue
		}
		fi, err := rc.Stat()
		rc.Close()
		if err != nil || fi.Size() == 0 {
			t.Errorf("embedded asset %s is empty or unstatable", name)
		}
	}

	index, _ := io.ReadAll(mustOpenDist(t, "index.html"))
	if !bytes.Contains(index, []byte("SHEYTAN")) || !bytes.Contains(index, []byte("main.js")) {
		t.Error("index.html must carry the product identity and the script reference")
	}
	script, _ := io.ReadAll(mustOpenDist(t, "main.js"))
	for _, marker := range []string{"/api/diagnostics", "/api/settings", "/api/transport", "/api/command", "tokens.json"} {
		if !bytes.Contains(script, []byte(marker)) {
			t.Errorf("main.js missing required endpoint reference %s", marker)
		}
	}
	css, _ := io.ReadAll(mustOpenDist(t, "sheytan.css"))
	if !bytes.Contains(css, []byte("--c-surface-obsidian")) || !bytes.Contains(css, []byte("prefers-reduced-motion")) {
		t.Error("sheytan.css must be tokens-driven and reduced-motion aware")
	}
}

func mustOpenDist(t *testing.T, name string) io.Reader {
	t.Helper()
	rc, err := dist.FS().Open(name)
	if err != nil {
		t.Fatalf("open dist asset %s: %v", name, err)
	}
	t.Cleanup(func() { rc.Close() })
	return rc
}

// TestAssetMuxFacadeHTTP is the acceptance-level facade test: the
// service + asset mux served through net/http/httptest, exercising the
// exact endpoints the frontend consumes. No webview, no display.
func TestAssetMuxFacadeHTTP(t *testing.T) {
	svc, _, _ := newTestService(t)
	server := httptest.NewServer(NewAssetHandler(svc))
	t.Cleanup(server.Close)

	get := func(path string) (int, []byte) {
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read body %s: %v", path, err)
		}
		return res.StatusCode, body
	}

	// Frontend entry point is served.
	code, body := get("/")
	if code != http.StatusOK {
		t.Errorf("GET / = %d, want 200", code)
	}
	if !bytes.Contains(body, []byte("SHEYTAN")) {
		t.Error("GET / should serve the SHEYTAN index.html")
	}

	// tokens.json served verbatim from the embedded copy.
	code, body = get("/tokens.json")
	if code != http.StatusOK || !bytes.Equal(body, design.Raw()) {
		t.Errorf("GET /tokens.json = %d, bytes differ from design.Raw() (%d vs %d)", code, len(body), len(design.Raw()))
	}

	// /api/diagnostics is valid JSON decoding into the versioned DTO.
	code, body = get("/api/diagnostics")
	if code != http.StatusOK {
		t.Fatalf("GET /api/diagnostics = %d: %s", code, body)
	}
	var diag presentation.DiagnosticsDTO
	if err := json.Unmarshal(body, &diag); err != nil {
		t.Fatalf("diagnostics is not valid JSON for the DTO: %v (%s)", err, body)
	}
	if diag.Build.Version == "" || diag.DataMode == "" || diag.Renderer.Backend == "" {
		t.Errorf("diagnostics snapshot incomplete: %+v", diag)
	}

	// /api/settings decodes into the versioned DTO with the default.
	code, body = get("/api/settings")
	if code != http.StatusOK {
		t.Fatalf("GET /api/settings = %d: %s", code, body)
	}
	var settings presentation.SettingsDTO
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf("settings is not valid JSON for the DTO: %v (%s)", err, body)
	}
	if settings.Intensity != design.MustLoad().DefaultIntensity {
		t.Errorf("settings intensity = %q, want %q", settings.Intensity, design.MustLoad().DefaultIntensity)
	}

	// /api/transport decodes with the facade default volume.
	code, body = get("/api/transport")
	if code != http.StatusOK {
		t.Fatalf("GET /api/transport = %d: %s", code, body)
	}
	var transport presentation.TransportDTO
	if err := json.Unmarshal(body, &transport); err != nil {
		t.Fatalf("transport is not valid JSON for the DTO: %v (%s)", err, body)
	}
	if transport.Volume != defaultVolume {
		t.Errorf("transport volume = %g, want %g", transport.Volume, defaultVolume)
	}

	// Unknown API endpoint is an explicit JSON 404, never HTML.
	code, body = get("/api/definitely-not-an-endpoint")
	if code != http.StatusNotFound {
		t.Errorf("GET /api/definitely-not-an-endpoint = %d, want 404", code)
	}
	if !bytes.Contains(body, []byte(`"error"`)) {
		t.Errorf("404 body should be the JSON error shape, got: %s", body)
	}

	post := func(payload string) (int, []byte) {
		res, err := http.Post(server.URL+"/api/command", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatalf("POST /api/command: %v", err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read command response: %v", err)
		}
		return res.StatusCode, b
	}

	// Unknown command over HTTP: typed JSON error, HTTP 400.
	code, body = post(`{"id":"totally.bogus"}`)
	if code != http.StatusBadRequest {
		t.Errorf("POST bogus command = %d, want 400", code)
	}
	var apiErr struct {
		Error string `json:"error"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal(body, &apiErr); err != nil || apiErr.Kind != KindUnknownCommand {
		t.Errorf("bogus command error body = %s (err %v), want kind %q", body, err, KindUnknownCommand)
	}

	// Unsupported command over HTTP: boundary reason attached.
	code, body = post(`{"id":"import.audio"}`)
	if code != http.StatusBadRequest {
		t.Errorf("POST import.audio = %d, want 400", code)
	}
	if err := json.Unmarshal(body, &apiErr); err != nil || apiErr.Kind != KindUnsupportedCommand {
		t.Errorf("import.audio error body = %s (err %v), want kind %q", body, err, KindUnsupportedCommand)
	}

	// Valid command over HTTP: ok:true, and the state change is visible
	// through the GET surface.
	code, body = post(`{"id":"transport.set-volume","args":{"volume":0.25}}`)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Errorf("POST set-volume = %d (%s), want 200 ok:true", code, body)
	}
	code, body = get("/api/transport")
	if code != http.StatusOK {
		t.Fatalf("GET /api/transport = %d", code)
	}
	if err := json.Unmarshal(body, &transport); err != nil {
		t.Fatalf("transport decode: %v", err)
	}
	if transport.Volume != 0.25 {
		t.Errorf("volume after command = %g, want 0.25", transport.Volume)
	}

	// Malformed body: explicit 400 JSON.
	code, body = post(`{not json}`)
	if code != http.StatusBadRequest {
		t.Errorf("POST malformed = %d, want 400", code)
	}
	if !bytes.Contains(body, []byte(`"error"`)) {
		t.Errorf("malformed body error shape missing: %s", body)
	}
}

// TestConcurrentFacadeAccess exercises the concurrency contract: every
// facade method must be safe for concurrent web calls (run with -race
// in CI). The renderer evidence is captured once under concurrency.
func TestConcurrentFacadeAccess(t *testing.T) {
	svc, _, _ := newTestService(t)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if err := svc.SetIntensity([]string{"subtle", "ritual", "infernal"}[i%3]); err != nil {
					errs <- fmt.Errorf("SetIntensity: %w", err)
					return
				}
				svc.SetReducedMotion(i%2 == 0)
				_ = svc.GetSettings()
				_ = svc.GetTransportSnapshot()
				_ = svc.GetPresets()
				_ = svc.GetLibrarySummary()
				_ = svc.GetRendererEvidence()
				if err := svc.RunCommand(string(presentation.CmdSetVolume), fmt.Sprintf(`{"volume":%g}`, float64(i%10)/10)); err != nil {
					errs <- fmt.Errorf("RunCommand: %w", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
