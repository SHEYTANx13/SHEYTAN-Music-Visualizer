// sheytan-music-visualizer is the entry point of the SHEYTAN Music
// Visualizer desktop application (Phase 1 foundation).
//
// Flags:
//
//	--version     print product/build identity and exit
//	--self-check  validate foundation invariants (build metadata, brand
//	              identity, writable app-log directory) and exit without
//	              opening a window
//
// The default mode starts the minimal Phase 1 Fyne window. Real startup
// errors return a non-zero process code; errors are never swallowed.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	sheytanui "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/ui/fyne"
)

func main() {
	os.Exit(run())
}

func run() int {
	version := flag.Bool("version", false, "print product/build identity and exit")
	selfCheckFlag := flag.Bool("self-check", false, "run foundation self-check and exit without opening a window")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "%s: unexpected argument %q\n", brand.ProductName, flag.Arg(0))
		return 2
	}

	switch {
	case *version:
		fmt.Printf("%s %s\n", brand.ProductName, buildinfo.Get())
		fmt.Println(brand.LegalFooter)
		return 0

	case *selfCheckFlag:
		return selfCheck()

	default:
		return launchWindow()
	}
}

// selfCheck verifies foundation invariants without opening a window.
// Every check prints a PASS/FAIL line; the process exits non-zero if any
// required check fails.
func selfCheck() int {
	failures := 0

	check := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Printf("FAIL %s: %v\n", name, err)
			return
		}
		fmt.Printf("PASS %s\n", name)
	}

	check("buildinfo", buildinfo.Validate())

	if brand.ProductName != "SHEYTAN Music Visualizer" {
		failures++
		fmt.Printf("FAIL brand-identity: product name drifted: %q\n", brand.ProductName)
	} else {
		fmt.Println("PASS brand-identity")
	}
	if brand.LegalFooter == "" || brand.TrademarkStatement == "" {
		failures++
		fmt.Println("FAIL brand-legal: legal footer or trademark statement empty")
	} else {
		fmt.Println("PASS brand-legal")
	}

	base, err := logging.DefaultBaseDir()
	if err != nil {
		check("app-log-dir", err)
		return finish(failures)
	}
	check("app-log-dir", logging.EnsureWritable(base))

	// Prove structured file logging end-to-end, then close cleanly.
	logger, err := logging.Open(base, logging.Options{})
	if err != nil {
		check("app-log-write", err)
		return finish(failures)
	}
	logger.Info("self-check executed", "phase", brand.PhaseCurrent)
	err = logger.Close()
	check("app-log-write", err)
	if err == nil {
		fmt.Printf("INFO log file: %s\n", logger.Path())
	}
	return finish(failures)
}

func finish(failures int) int {
	if failures > 0 {
		fmt.Printf("SELF-CHECK RESULT: FAIL (%d failing check(s))\n", failures)
		return 1
	}
	fmt.Println("SELF-CHECK RESULT: PASS")
	return 0
}

// launchWindow starts the minimal Phase 1 Fyne window. Log setup problems
// are reported visibly on stderr; they do not abort the GUI run, but the
// logger falls back to stderr so logs are never silently dropped.
func launchWindow() int {
	base, err := logging.DefaultBaseDir()
	var logger *logging.Logger
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING app log unavailable (%v); falling back to stderr logging\n", err)
		logger = logging.NewStderr(os.Stderr)
	} else {
		logger, err = logging.Open(base, logging.Options{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING app log unavailable (%v); falling back to stderr logging\n", err)
			logger = logging.NewStderr(os.Stderr)
		}
	}
	defer logger.Close()

	logger.Info("starting", "phase", brand.PhaseCurrent, "version", buildinfo.Get().Version)
	if err := sheytanui.Run(); err != nil {
		logger.Error("window startup failed", "error", err.Error())
		fmt.Fprintf(os.Stderr, "ERROR %v\n", err)
		return 1
	}
	logger.Info("window closed cleanly")
	return 0
}
