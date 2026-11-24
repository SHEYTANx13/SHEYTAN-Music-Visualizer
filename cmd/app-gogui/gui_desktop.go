// gui_desktop.go — the desktop entry of the native Go-Gui shell
// (windows, linux, darwin): the recoverable-panic boundary around the
// ONE gogui window lifecycle. The mobile targets (android, iOS) run
// the x/mobile lifecycle loop instead — see gui_mobile.go.
//
//go:build !android && !ios

package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/ui/gogui"
)

// runGUI launches the full window with a recoverable-panic boundary;
// gui_mobile.go provides the android/iOS x/mobile lifecycle entry
// instead. The bootstrap log rides into Run so every shell phase is
// recorded in app.log.
func runGUI(boot *logging.Log) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unexpected panic in shell runtime: %v", r)
			fmt.Fprintf(os.Stderr, "%s: PANIC (recoverable): %v\n%s", brand.ProductName, r, debug.Stack())
		}
	}()
	return gogui.Run(gogui.RunOptions{Log: boot})
}
