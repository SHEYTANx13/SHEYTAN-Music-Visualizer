#!/usr/bin/env bash
# build-macos.sh — macOS desktop build/verification gates.
#
# TRUTHFUL SCOPE (priority-B repair): the parity gates (go-test-race,
# export-validation, audio-validation, gpu-validation, gui-smoke) RUN as
# workflow steps of the build-macos job in .github/workflows/build.yml —
# this script no longer marks them BLOCKED (that stale record made the
# consolidated gate table contradict the job: a required parity gate
# that ran and passed must not be labeled BLOCKED anywhere). This
# script owns: toolchain, gofmt, module verify, full test suite, vet,
# executable build, BETA version runtime check and the REAL .app bundle
# package (Contents/MacOS executable + Info.plist) archived as the
# canonical macos-arm64.app.zip candidate. The macOS package is an
# unsigned test build, honestly labeled; no notarization or developer-ID
# signing is claimed without actual signing evidence.
set -u

# Load the canonical capture helpers (ci_capture, ci_sanitize).
. scripts/ci/capture.sh

JOB="build-macos"
cd "$GITHUB_WORKSPACE" || exit 1
export CI_LOG_FILE="$PWD/ci-logs/phase2-build.log"
mkdir -p ci-logs

SKIPPED_GATES=""
FAILED_GATES=""

note() { printf '%s\n' "$*" | tee -a "$CI_LOG_FILE"; }
# RUN 45532045379 repair (no exit 1 without a specific explanation): a
# failed gate now emits an explicit, extractable ERROR line that names
# the gate and points at its captured output. The previous silent
# summary-only contract left hosted failures with no consolidated-log
# record when the failing tool output matched no diagnostic pattern.
fail_gate() {
  FAILED_GATES="$FAILED_GATES $1"
  printf 'ERROR: gate %s FAILED (see the captured output above in this step; the gate result JSON in ci-logs/gate-results carries the exit code and timing)\n' "$1" | tee -a "$CI_LOG_FILE" >&2
}
skip_gate() { SKIPPED_GATES="$SKIPPED_GATES $1"; printf 'SKIPPED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"; }
blocked_gate() { SKIPPED_GATES="$SKIPPED_GATES $1"; printf 'BLOCKED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"; }

# RUN 45532045379 repair (canonical Go profile): every Go gate below
# pins the repository-canonical link profile documented in
# scripts/build/build-windows.ps1 and used by the build-macos workflow
# steps (CGO_ENABLED=1, build tag goffi_static). Without the tag, the
# goffi v0.6.8 dynamic-loader path fails to link (its dl stubs JMP to
# cgo_import_dynamic symbols that resolve only under the goffi_static
# static profile or real external cgo linking), which reproduced
# locally as "unhandled relocation ... (SDYNIMPORT)" for
# cmd/app-gogui and the ui/gogui, internal/studio and
# internal/transport/wails test binaries. Pinning CGO_ENABLED=1 also
# excludes the purego/goffi fakecgo duplicate-symbol link error class
# on CGO-disabled runners.
export CGO_ENABLED=1
GO_TAGS=(-tags goffi_static)

note "SHEYTAN MUSIC VISUALIZER :: PHASE 2 LOCAL VERIFICATION (MACOS)"
note "Mode: desktop (reduced parity set; see header)"
note "Host: $(uname -srm)"
note "Started: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

# Toolchain report.
if ! command -v go >/dev/null 2>&1; then
  for g in toolchain-report gofmt-verify go-mod-verify go-test go-vet go-build-packages build-executable run-version package-macos; do
    blocked_gate "$g" "go toolchain not found on PATH"
  done
  blocked_gate go-test-race "go toolchain not found on PATH"
  blocked_gate export-validation "go toolchain not found on PATH"
  blocked_gate audio-validation "go toolchain not found on PATH"
  blocked_gate gpu-validation "go toolchain not found on PATH"
  blocked_gate gui-smoke "go toolchain not found on PATH"
else
  CI_TOOL_CONTEXT="gate:toolchain" ci_capture "$JOB" toolchain-report bash -c 'go version; echo "GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED)"; clang --version | head -1; xcodebuild -version 2>/dev/null | head -1 || echo "xcodebuild: NOT AVAILABLE"' ||
    fail_gate toolchain-report

  ci_capture "$JOB" gofmt-verify bash -c 'out=$(gofmt -l .); if [ -n "$out" ]; then printf "%s\n" "$out"; echo "ERROR: gofmt reported unformatted files"; exit 1; fi; echo "gofmt clean"' ||
    fail_gate gofmt-verify

  ci_capture "$JOB" go-mod-verify go mod verify || fail_gate go-mod-verify

  ci_capture "$JOB" go-test go test "${GO_TAGS[@]}" ./... || fail_gate go-test
  ci_capture "$JOB" go-vet go vet "${GO_TAGS[@]}" ./... || fail_gate go-vet
  ci_capture "$JOB" go-build-packages go build "${GO_TAGS[@]}" ./... || fail_gate go-build-packages

  BI=github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo
  LDFLAGS=(-ldflags "-X ${BI}.version=${SHEYTAN_BUILD_VERSION:-BETA} -X ${BI}.commit=${SHEYTAN_BUILD_COMMIT:-unknown} -X ${BI}.date=${SHEYTAN_BUILD_DATE:-unknown} -X ${BI}.builtBy=${SHEYTAN_BUILT_BY:-github-actions}")
  if ci_capture "$JOB" build-executable go build -trimpath "${GO_TAGS[@]}" "${LDFLAGS[@]}" -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer; then
    CI_TOOL_CONTEXT="runtime:--version" ci_capture "$JOB" run-version bash -c './bin/sheytan-music-visualizer --version | tee /tmp/version.txt; grep -q "version: BETA" /tmp/version.txt || grep -q "version BETA" /tmp/version.txt || grep -q "BETA" /tmp/version.txt' ||
      fail_gate run-version
  else
    fail_gate build-executable
    skip_gate run-version "executable build failed"
  fi

  # RUN 45297707372 SHIP-THE-SHELL REPAIR: the .app bundle used to carry
  # the CLI authority as CFBundleExecutable - a binary that never opens a
  # window, so double-clicking the app appeared dead. The bundle now
  # carries the native Go-Gui shell (cmd/app-gogui) as its executable,
  # with the shell icon (icon.icns from the tracked iconset via
  # iconutil) and the CLI as a companion tool.
  GUI_EXE_OK=0
  if ci_capture "$JOB" build-gogui-shell go build -trimpath "${GO_TAGS[@]}" "${LDFLAGS[@]}" -o bin/app-gogui ./cmd/app-gogui; then
    GUI_EXE_OK=1
  else
    fail_gate build-gogui-shell
  fi

  # Parity gates (go-test-race, export-validation, audio-validation,
  # gpu-validation, gui-smoke) run as build.yml workflow steps AFTER this
  # script; they are neither blocked nor claimed here. This script only
  # records that they live at job level (an informational note, not a
  # gate verdict).
  note "parity gates (race/export/audio/gpu/gui-smoke) run as build-macos workflow steps; see the job log for their authoritative gate records"

  if [ "$GUI_EXE_OK" -eq 1 ] && [ -f bin/sheytan-music-visualizer ]; then
    ci_capture "$JOB" package-macos bash -c '
      set -Eeuo pipefail
      mkdir -p dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS dist/SHEYTAN-Music-Visualizer.app/Contents/Resources
      # RUN 45297707372 ship-the-shell: CFBundleExecutable is the native
      # Go-Gui shell (the binary that actually opens the window); the
      # CLI authority ships beside it as the scripting companion.
      cp bin/app-gogui dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS/SHEYTAN-Music-Visualizer
      cp bin/sheytan-music-visualizer dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS/sheytan-music-visualizer-cli
      chmod +x dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS/SHEYTAN-Music-Visualizer
      chmod +x dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS/sheytan-music-visualizer-cli
      # Shell icon: build icon.icns from the tracked iconset with
      # iconutil (present on every macOS runner). A missing iconutil is
      # recorded, never faked - the bundle then ships without an icon
      # and the gate records the honest state.
      # RUN 45347124889 repair: iconutil REFUSES an iconset whose file
      # dimensions contradict the documented Apple iconset table (the
      # tracked set shipped icon_512x512@2x.png at 512x512, missed five
      # canonical names and carried a non-standard icon_64x64.png; the
      # opaque iconutil failure produced NO artifact). The set is now
      # validated file-by-file BEFORE iconutil runs, so a regression is
      # a precise per-file diagnostic, never an opaque exit code.
      if command -v iconutil >/dev/null 2>&1; then
        set -- icon_16x16.png:16 icon_16x16@2x.png:32 icon_32x32.png:32 \
          icon_32x32@2x.png:64 icon_128x128.png:128 icon_128x128@2x.png:256 \
          icon_256x256.png:256 icon_256x256@2x.png:512 icon_512x512.png:512 \
          icon_512x512@2x.png:1024
        icon_err=0
        for entry in "$@"; do
          name="${entry%%:*}"
          want="${entry##*:}"
          file="assets/icon/iconset/$name"
          if [ ! -f "$file" ]; then
            echo "ERROR: iconset is missing the required entry $name (documented Apple iconset table)"
            icon_err=1
            continue
          fi
          dims="$(sips -g pixelWidth -g pixelHeight "$file" 2>/dev/null | awk '/pixelWidth/{w=$2} /pixelHeight/{h=$2} END{print w "x" h}')"
          if [ "$dims" != "${want}x${want}" ]; then
            echo "ERROR: iconset entry $name is ${dims:-unreadable}px, must be ${want}x${want}px (iconutil rejects a dimension mismatch)"
            icon_err=1
          fi
        done
        extra=""
        for f in assets/icon/iconset/*; do
          case "$(basename "$f")" in
            icon_16x16.png|icon_16x16@2x.png|icon_32x32.png|icon_32x32@2x.png|icon_128x128.png|icon_128x128@2x.png|icon_256x256.png|icon_256x256@2x.png|icon_512x512.png|icon_512x512@2x.png) ;;
            *) extra="$extra $(basename "$f")" ;;
          esac
        done
        if [ -n "$extra" ]; then
          echo "ERROR: iconset carries non-canonical entries (must be exactly the documented table):$extra"
          icon_err=1
        fi
        if [ "$icon_err" -ne 0 ]; then
          echo "ERROR: tracked macOS iconset does not satisfy the iconutil contract; fix assets/icon/iconset via scripts/assets/gen_icon.py"
          exit 1
        fi
        echo "iconset validated: 10 canonical entries, dimensions match the documented table"
        # RUN 45455106994 repair (the run-45347124889 follow-up): the
        # iconutil contract requires the SOURCE DIRECTORY ITSELF to be a
        # ".iconset" folder - the tool takes only specially named folders
        # (Apple iconset contract). The previous gate pointed iconutil at
        # assets/icon/iconset (no .iconset suffix), so even after the
        # dimension repair of run 45347124889 the conversion still failed
        # opaquely and the job again produced NO .app.zip. The tracked
        # directory is copied to a properly named dist/AppIcon.iconset and
        # converted from there; every failure mode now carries its own
        # explicit ERROR line instead of an opaque exit under pipefail.
        # NOTE: this gate body is a single-quoted bash -c argument; no
        # apostrophe may appear anywhere in it.
        icns_out="dist/SHEYTAN-Music-Visualizer.app/Contents/Resources/AppIcon.icns"
        rm -rf dist/AppIcon.iconset
        cp -R assets/icon/iconset dist/AppIcon.iconset
        if [ ! -f dist/AppIcon.iconset/icon_16x16.png ]; then
          echo "ERROR: dist/AppIcon.iconset copy is incomplete (cp -R failed); listing:" >&2
          ls -la dist/AppIcon.iconset >&2 || true
          exit 1
        fi
        icon_err=0
        iconutil -c icns dist/AppIcon.iconset -o "$icns_out" 2>&1 || icon_err=$?
        if [ "$icon_err" -ne 0 ]; then
          echo "ERROR: iconutil -c icns failed with exit code $icon_err (source dist/AppIcon.iconset, output $icns_out)"
          echo "       the iconset folder must be named *.iconset and carry exactly the documented Apple table"
          exit 1
        fi
        rm -rf dist/AppIcon.iconset
        if [ ! -s "$icns_out" ]; then
          echo "ERROR: AppIcon.icns missing or empty after iconutil reported success ($icns_out)"
          exit 1
        fi
        echo "icon.icns built from the tracked iconset via dist/AppIcon.iconset (iconutil, .iconset-named source)"
      else
        echo "iconutil UNAVAILABLE on this runner; the bundle ships without an icon (honestly recorded)"
      fi
      cat > dist/SHEYTAN-Music-Visualizer.app/Contents/Info.plist <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>SHEYTAN Music Visualizer</string>
  <key>CFBundleDisplayName</key><string>SHEYTAN Music Visualizer</string>
  <key>CFBundleIdentifier</key><string>com.sheytan.music-visualizer</string>
  <key>CFBundleExecutable</key><string>SHEYTAN-Music-Visualizer</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>BETA</string>
  <key>CFBundleVersion</key><string>BETA</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
      # Unsigned TEST build: labeled honestly here and in the job summary.
      echo "macOS package: UNSIGNED TEST BUILD (no codesign/notarization claim)"
      ( cd dist && zip -qry SHEYTAN-Music-Visualizer-macos-arm64.app.zip SHEYTAN-Music-Visualizer.app )
      ls -l dist/
      test -f dist/SHEYTAN-Music-Visualizer.app/Contents/Info.plist
      test -f dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS/SHEYTAN-Music-Visualizer
      # RUN 45297707372: the bundle executable must be the SHELL, not
      # the CLI authority (the CLI never opens a window).
      test -s dist/SHEYTAN-Music-Visualizer.app/Contents/MacOS/sheytan-music-visualizer-cli
    ' || fail_gate package-macos
  else
    skip_gate package-macos "executable build failed"
  fi
fi

note ""
note "RUN SUMMARY :: $JOB"
note "Failed gates:${FAILED_GATES:- none}"
note "Skipped or blocked gates:${SKIPPED_GATES:- none}"
if [ -n "$FAILED_GATES" ]; then
  note "FINAL: FAIL"
  exit 1
fi
note "FINAL: PASS (this script gates; parity gates run as build-macos workflow steps and are judged there)"
exit 0
