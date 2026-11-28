package wails

// host.go — the native host capability surface of the web shell.
//
// The facade boundary rules (doc.go) forbid arbitrary filesystem,
// shell and path access from the web layer. Importing audio and
// choosing an export destination still need a NATIVE file dialog:
// the user picks the file in an OS-owned dialog, Go receives the
// path directly, and the path NEVER crosses the boundary into
// JavaScript. The web layer only signals the intent ("import.audio",
// "export.start"); everything else is native.
//
// This file holds the instance-scoped interface and a function-based
// adapter so the facade stays headless-testable: without a host
// registered, dialog-backed commands return a typed FacadeError that
// states the exact reason (the same pattern as the quit handler).

// hostDialogs is the native dialog capability surface. Implementations
// must block until the dialog closes (or an error/cancel occurs) and
// return an empty string for a user cancel - a cancel is never an
// error and never fakes success.
type hostDialogs interface {
	// OpenAudioPath shows the native open-file dialog for an audio
	// import. Returns "" when the user cancelled.
	OpenAudioPath() (string, error)

	// SaveExportPath shows the native save-file dialog for the MP4
	// export destination. defaultName is a suggested file name only;
	// implementations must not treat it as a path. Returns "" when the
	// user cancelled.
	SaveExportPath(defaultName string) (string, error)

	// SaveBundlePath shows the native save-file dialog for the
	// diagnostics bundle destination (run 46168717789 log catcher).
	// Returns "" when the user cancelled.
	SaveBundlePath(defaultName string) (string, error)

	// OpenSubtitlePath shows the native open-file dialog for a subtitle
	// attachment (export burn-in). Returns "" when the user cancelled.
	OpenSubtitlePath() (string, error)
}

// HostDialogsFuncs adapts two functions to hostDialogs. app.go builds
// it over the real Wails v3 dialog runtime; tests build it over stubs.
type HostDialogsFuncs struct {
	OpenAudio    func() (string, error)
	SaveExport   func(defaultName string) (string, error)
	SaveBundle   func(defaultName string) (string, error)
	OpenSubtitle func() (string, error)
}

// OpenAudioPath implements hostDialogs.
func (f HostDialogsFuncs) OpenAudioPath() (string, error) {
	if f.OpenAudio == nil {
		return "", errDialogsUnavailable
	}
	return f.OpenAudio()
}

// SaveExportPath implements hostDialogs.
func (f HostDialogsFuncs) SaveExportPath(defaultName string) (string, error) {
	if f.SaveExport == nil {
		return "", errDialogsUnavailable
	}
	return f.SaveExport(defaultName)
}

// SaveBundlePath implements hostDialogs.
func (f HostDialogsFuncs) SaveBundlePath(defaultName string) (string, error) {
	if f.SaveBundle == nil {
		return "", errDialogsUnavailable
	}
	return f.SaveBundle(defaultName)
}

// OpenSubtitlePath implements hostDialogs.
func (f HostDialogsFuncs) OpenSubtitlePath() (string, error) {
	if f.OpenSubtitle == nil {
		return "", errDialogsUnavailable
	}
	return f.OpenSubtitle()
}

// errDialogsUnavailable is the typed reason used when the capability
// is invoked without a registered implementation.
var errDialogsUnavailable = &FacadeError{
	Kind:   KindRejected,
	Reason: "no native dialog host registered (headless or test context cannot open OS dialogs)",
}
