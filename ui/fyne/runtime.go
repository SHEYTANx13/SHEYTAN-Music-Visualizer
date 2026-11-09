// runtime.go — runtime bootstrap for the Fyne surface: the single
// startup path that resolves the data layout, creates the app-owned
// directories, migrates the legacy log non-destructively and opens the
// runtime log BEFORE any subsystem or window construction. Also hosts
// the fullscreen.Window adapter that connects the framework-agnostic
// controller to fyne.Window.
//
// Contract: a broken log file never takes the product down — Init
// returns a stderr-backed logger with the surfaced error, and the
// resolved fallback is visible in the Help/Diagnostics surface.

package fyneapp

import (
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fullscreen"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
)

// initRuntime resolves the layout, bootstraps the directory contract,
// copies (never moves) a legacy app.log if one exists and opens the
// runtime log. It ALWAYS returns a usable logger (stderr fallback on
// failure) together with the error for the caller to surface.
func initRuntime() (*logging.Log, apppaths.Layout, error) {
	lay := apppaths.Resolve(apppaths.OSEnv())
	debug := os.Getenv("SHEYTAN_DEBUG") != ""

	// Directory bootstrap failures are recorded but do not stop the
	// launch attempt: the log fallback keeps evidence flowing.
	bootErr := apppaths.Bootstrap(lay)

	lg, using, initErr := logging.Init(lay, debug)
	if initErr != nil {
		// Init already switched to the stderr fallback and logged the
		// exact error; keep the error so the UI can surface it.
		return lg, lay, initErr
	}
	if bootErr != nil {
		lg.Logger().Error("data directory bootstrap incomplete",
			"err", bootErr.Error(), "data_root", lay.DataRoot)
	}
	if migrated, err := logging.MigrateLegacy(lay, apppaths.OSEnv()); err != nil {
		lg.Logger().Warn("legacy log migration failed", "err", err.Error())
	} else if migrated != "" {
		lg.Logger().Info("legacy runtime log preserved by copy", "from", migrated)
	}
	_ = using // non-fallback path: using == log.Path()
	return lg, lay, nil
}

// fsWindow adapts fyne.Window to the framework-agnostic
// fullscreen.Window interface.
type fsWindow struct {
	w fyne.Window
}

func (f fsWindow) FullScreen() bool      { return f.w.FullScreen() }
func (f fsWindow) SetFullScreen(on bool) { f.w.SetFullScreen(on) }

func (f fsWindow) ContentSize() fullscreen.Size {
	s := f.w.Content().Size()
	return fullscreen.Size{Width: s.Width, Height: s.Height}
}

func (f fsWindow) Resize(s fullscreen.Size) {
	f.w.Resize(fyne.NewSize(s.Width, s.Height))
}

// windowStateStorePath is the persisted fullscreen intent location
// inside the resolved config directory.
func windowStateStorePath(lay apppaths.Layout) string {
	return filepath.Join(lay.Config, "window-state.json")
}
