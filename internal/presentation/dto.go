package presentation

import "time"

// Versioned DTOs (v1) crossing the core-to-shell boundary. Domain
// structs, project records and GPU frames are NEVER exposed directly
// to a shell; every crossing uses these explicit, JSON-tagged values
// so both shells render from identical data.

const DTOVersion = 1

// BuildInfoDTO is the build identity snapshot.
type BuildInfoDTO struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	BuiltBy   string `json:"builtBy"`
	GoVersion string `json:"goVersion"`
}

// TransportDTO is the bounded transport state snapshot. It is the only
// transport shape the shells may render; clock telemetry flows through
// the coalesced latest-value path of the event bus.
type TransportDTO struct {
	State      string        `json:"state"`
	Position   time.Duration `json:"position"`
	Total      time.Duration `json:"total"`
	Latency    time.Duration `json:"latency"`
	DeviceReal bool          `json:"deviceReal"`
	Session    uint64        `json:"session"`
	Volume     float64       `json:"volume"`
}

// RendererDTO reports renderer evidence honestly. GpuState carries the
// exact state machine values from internal/render (none, detected,
// available, selected, executed); a shell must never upgrade a state.
type RendererDTO struct {
	Backend     string `json:"backend"`
	GpuState    string `json:"gpuState"`
	BackendAPI  string `json:"backendApi"`
	Adapter     string `json:"adapter"`
	DeviceType  string `json:"deviceType"`
	DeviceClass string `json:"deviceClass"`
	Detail      string `json:"detail"`
}

// LibraryItemDTO is one library entry (song or project file).
type LibraryItemDTO struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Kind     string    `json:"kind"` // "song" or "project"
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
	Duration float64   `json:"duration,omitempty"` // seconds, when probed
}

// ExportRequestDTO starts one deterministic export. All fields are
// clamped and validated by the core before any work begins.
type ExportRequestDTO struct {
	Out     string  `json:"out"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	FPS     int     `json:"fps"`
	Seconds float64 `json:"seconds"`
	Codec   string  `json:"codec"`
}

// ExportProgressDTO is the coalesced export progress snapshot.
type ExportProgressDTO struct {
	FramesDone int     `json:"framesDone"`
	FramesMax  int     `json:"framesMax"`
	Stage      string  `json:"stage"`
	Fraction   float64 `json:"fraction"`
}

// SettingsDTO is the shell-visible settings snapshot.
type SettingsDTO struct {
	Intensity     string `json:"intensity"` // subtle|ritual|infernal
	ReducedMotion bool   `json:"reducedMotion"`
	ReducedGlow   bool   `json:"reducedGlow"`
	RendererPref  string `json:"rendererPref"` // auto|gpu|cpu
}

// WorkspaceLayoutDTO is the versioned persisted workspace layout
// (tab order, split orientation, collapsed panels). Version enables
// migration; corrupt files recover to DefaultLayout.
type WorkspaceLayoutDTO struct {
	Version       int           `json:"version"`
	TabOrder      []Destination `json:"tabOrder"`
	ActiveTab     Destination   `json:"activeTab"`
	SplitVertical bool          `json:"splitVertical"`
	Collapsed     []Destination `json:"collapsed"`
}

// DefaultLayout returns the canonical default workspace layout.
func DefaultLayout() WorkspaceLayoutDTO {
	order := make([]Destination, len(Destinations))
	copy(order, Destinations)
	return WorkspaceLayoutDTO{
		Version:       1,
		TabOrder:      order,
		ActiveTab:     DestHome,
		SplitVertical: false,
		Collapsed:     nil,
	}
}

// DiagnosticsDTO aggregates the diagnostics surface in one snapshot.
type DiagnosticsDTO struct {
	Build      BuildInfoDTO   `json:"build"`
	DataMode   string         `json:"dataMode"`
	DataRoot   string         `json:"dataRoot"`
	LogPath    string         `json:"logPath"`
	Renderer   RendererDTO    `json:"renderer"`
	Settings   SettingsDTO    `json:"settings"`
	CheckedAt  time.Time      `json:"checkedAt"`
	SelfChecks []SelfCheckDTO `json:"selfChecks"`
}

// SelfCheckDTO is one self-check result (name + pass/fail + detail).
type SelfCheckDTO struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}
