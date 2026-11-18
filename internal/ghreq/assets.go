package ghreq

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

// Asset selection failures.
var (
	ErrPlatformUnsupported = errors.New("ghreq: requirement not available for this platform")
	ErrAmbiguousAssets     = errors.New("ghreq: ambiguous asset selection")
	ErrAssetState          = errors.New("ghreq: asset not in uploaded state")
)

// NormalizePlatform maps runtime.GOOS/GOARCH onto the catalog's
// normalized platform identifiers. It never treats arm64 and amd64 as
// interchangeable and never aliases libc variants that the catalog
// does not model.
func NormalizePlatform(goos, goarch string) string {
	os := normalizeOS(goos)
	arch := normalizeArch(goarch)
	if os == "" || arch == "" {
		return ""
	}
	return os + "/" + arch
}

// PlatformForHost returns the normalized platform of this process.
func PlatformForHost() string {
	return NormalizePlatform(runtime.GOOS, runtime.GOARCH)
}

func normalizeOS(goos string) string {
	switch goos {
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	case "android":
		return "android"
	case "ios":
		return "ios"
	default:
		return ""
	}
}

func normalizeArch(goarch string) string {
	switch goarch {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return ""
	}
}

// SelectAsset applies the requirement's deterministic selector for the
// given platform to the release asset list. Rules:
//
//  1. No rule for the platform: ErrPlatformUnsupported
//     (NOT_AVAILABLE_FOR_THIS_PLATFORM), never a guess.
//  2. Assets not in the "uploaded" state are ignored.
//  3. An asset qualifies only when its name matches the anchored
//     include selector and no anchored exclude selector.
//  4. Exactly one qualifying asset is required; zero or more than one
//     is an error, never a silent first-pick.
func SelectAsset(rel *Release, req *Requirement, platform string) (*Asset, error) {
	var rule *AssetRule
	for i := range req.AssetRules {
		if req.AssetRules[i].Platform == platform {
			rule = &req.AssetRules[i]
			break
		}
	}
	if rule == nil {
		return nil, fmt.Errorf("%w: %s (%s)", ErrPlatformUnsupported, req.ID, platform)
	}
	inc := regexp.MustCompile(rule.IncludeRegex)
	var ex []*regexp.Regexp
	for _, e := range rule.ExcludeRegex {
		ex = append(ex, regexp.MustCompile(e))
	}
	var matches []*Asset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		if a.State != "" && a.State != "uploaded" {
			continue
		}
		if !inc.MatchString(a.Name) {
			continue
		}
		excluded := false
		for _, re := range ex {
			if re.MatchString(a.Name) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		matches = append(matches, a)
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %s (%s)", ErrPlatformUnsupported, req.ID, platform)
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		return nil, fmt.Errorf("%w: %s (%s): %s", ErrAmbiguousAssets, req.ID, platform, strings.Join(names, ", "))
	}
}

// LooksLikeHTMLErrorPage reports whether the leading bytes of a
// downloaded payload are an HTML document rather than the expected
// binary asset. GitHub outages and auth walls answer with HTML; such
// payloads must never be treated as packages.
func LooksLikeHTMLErrorPage(head []byte) bool {
	const probe = 512
	if len(head) > probe {
		head = head[:probe]
	}
	s := strings.ToLower(string(head))
	for _, tag := range []string{"<!doctype html", "<html", "<head>", "<body"} {
		if strings.Contains(s, tag) {
			return true
		}
	}
	return false
}
