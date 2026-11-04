// Command sheytan-music-visualizer is the desktop application entry
// point for SHEYTAN Music Visualizer.
//
// Usage:
//
//	sheytan-music-visualizer              start the minimal Fyne window
//	sheytan-music-visualizer --version    print product/build metadata
//	sheytan-music-visualizer --self-check verify foundation invariants
//	                                      and exit without a window
//
// Real startup errors return a non-zero process exit code; errors are
// never swallowed.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	fyneapp "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/ui/fyne"
)

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errW io.Writer) int {
	fs := flag.NewFlagSet(brand.ProductName, flag.ContinueOnError)
	fs.SetOutput(errW)
	showVersion := fs.Bool("version", false, "print product and build metadata, then exit")
	selfCheckFlag := fs.Bool("self-check", false, "run foundation self-checks, then exit without opening a window")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errW, "%s: unexpected arguments: %s\n", brand.ProductName, strings.Join(fs.Args(), " "))
		return 2
	}
	switch {
	case *showVersion:
		fmt.Fprintf(out, "%s %s\n", brand.ProductName, buildinfo.Line())
		fmt.Fprintf(out, "%s\n%s\n", brand.Copyright, brand.SourceURL)
		return 0
	case *selfCheckFlag:
		return selfCheck(out, errW)
	default:
		if err := fyneapp.Run(); err != nil {
			fmt.Fprintf(errW, "%s: startup failed: %v\n", brand.ProductName, err)
			return 1
		}
		return 0
	}
}

type check struct {
	name string
	fn   func(io.Writer) error
}

func selfCheck(out, errW io.Writer) int {
	checks := []check{
		{"buildinfo", checkBuildInfo},
		{"brand-identity", checkBrandIdentity},
		{"log-directory", checkLogDirectory},
	}
	failures := 0
	for _, c := range checks {
		if err := c.fn(out); err != nil {
			failures++
			fmt.Fprintf(errW, "self-check FAIL %s: %v\n", c.name, err)
			continue
		}
		fmt.Fprintf(out, "self-check PASS %s\n", c.name)
	}
	if failures > 0 {
		fmt.Fprintf(errW, "self-check: %d check(s) failed\n", failures)
		return 1
	}
	fmt.Fprintln(out, "self-check: all foundation invariants hold")
	return 0
}

func checkBuildInfo(out io.Writer) error {
	i := buildinfo.Normalized()
	for name, v := range map[string]string{
		"version": i.Version, "commit": i.Commit, "date": i.Date, "builtBy": i.BuiltBy, "go": i.GoVersion,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("buildinfo.%s is empty", name)
		}
	}
	fmt.Fprintf(out, "  version=%s commit=%s go=%s\n", i.Version, i.Commit, i.GoVersion)
	return nil
}

func checkBrandIdentity(out io.Writer) error {
	if brand.ProductName == "" || brand.AppID == "" || brand.ModulePath == "" {
		return fmt.Errorf("core identity constants must be non-empty")
	}
	if strings.ContainsAny(brand.ProductName, "\n\r\t") {
		return fmt.Errorf("product name must be single-line")
	}
	for token, value := range brand.Palette() {
		if !hexColor.MatchString(value) {
			return fmt.Errorf("palette token %s = %q is not #RRGGBB", token, value)
		}
	}
	return nil
}

func checkLogDirectory(out io.Writer) error {
	dir, err := logging.Dir()
	if err != nil {
		return fmt.Errorf("resolve log directory: %w", err)
	}
	if err := logging.VerifyWritable(dir); err != nil {
		return fmt.Errorf("log directory not writable: %w", err)
	}
	l, err := logging.Open(dir, logging.Options{})
	if err != nil {
		return fmt.Errorf("open application log: %w", err)
	}
	defer l.Close()
	l.Logger().Info("self-check executed", "component", "cmd/sheytan-music-visualizer")
	fmt.Fprintf(out, "  log=%s\n", l.Path())
	return nil
}
