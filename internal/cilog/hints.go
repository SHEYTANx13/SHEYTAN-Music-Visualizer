package cilog

// hints.go — known-root-cause hints for the consolidated diagnostics
// artifact (run-45042834042 format upgrade: "better output format for
// faster and more accurate updates").
//
// Every record whose message matches a known failure signature carries
// a machine-grepable hint= field naming the NEXT ACTION, so triage
// reads the action instead of re-deriving it from prose. Rules are
// deterministic (first match in declaration order wins), ASCII-only by
// construction, and intentionally conservative: only failure classes
// that actually occurred in this project's CI history get a rule.

import "regexp"

type hintRule struct {
	match *regexp.Regexp
	hint  string
}

var hintRules = []hintRule{
	// Go compiler constant overflow on 32-bit ABIs (run 45042834042,
	// build-android: untyped 3 GiB constant passed to fmt.Errorf).
	{
		match: regexp.MustCompile(`\(overflows\)`),
		hint:  "widen-the-constant-to-int64-value-exceeds-32-bit-int",
	},
	// Rotated/missing pinned BtbN autobuild release (run 45042834042,
	// build-linux + build-windows media-tools gates).
	{
		match: regexp.MustCompile(`(?i)pinned (FFmpeg download failed|media-tools provisioning failed|asset).*|(?i)response status code does not indicate success: 404`),
		hint:  "rotate-btbn-autobuild-pin-see-packaging-FFMPEG-TOOLS-PROVENANCE",
	},
	// Required APK not produced (run 45042834042, build-android gate).
	{
		match: regexp.MustCompile(`APK missing or empty`),
		hint:  "apk-not-produced-inspect-android-build-steps-above",
	},
	// Artifact upload found nothing at the given path.
	{
		match: regexp.MustCompile(`(?i)no files were found with the provided path`),
		hint:  "artifact-path-missing-check-upstream-package-step",
	},
	// CodeQL extractor saw no compiled source (historical run 45005960383).
	{
		match: regexp.MustCompile(`(?i)run didn't build any of it|could not process any of it`),
		hint:  "codeql-saw-no-compile-actions-check-build-cache-prewarming",
	},
}

// hintFor returns the known-root-cause next action for a diagnostic
// message, or "" when the message matches no known signature. The tool
// parameter is kept for future tool-scoped rules; hints are currently
// message-derived only (deterministic across collectors).
func hintFor(tool, message string) string {
	for _, rule := range hintRules {
		if rule.match.MatchString(message) {
			return rule.hint
		}
	}
	_ = tool
	return ""
}
