package ghreq

import (
	"errors"
	"testing"
)

func relWithAssets(assets ...Asset) *Release {
	r := stubRelease("v1.0.0", false, false, assets...)
	return &r
}

func TestSelectAssetDeterministicMatch(t *testing.T) {
	req := &Requirement{
		ID: "t",
		AssetRules: []AssetRule{{
			Platform:     "windows/amd64",
			IncludeRegex: "^tool-win64\\.zip$",
			ExcludeRegex: []string{`^.*\.sig$`, `^.*-debug\.zip$`},
		}},
	}
	rel := relWithAssets(
		Asset{Name: "tool-win64.zip", State: "uploaded", Size: 100},
		Asset{Name: "tool-win64.zip.sig", State: "uploaded", Size: 1},
		Asset{Name: "tool-win64-debug.zip", State: "uploaded", Size: 500},
		Asset{Name: "tool-src.tar.gz", State: "uploaded", Size: 900},
	)
	a, err := SelectAsset(rel, req, "windows/amd64")
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	if a.Name != "tool-win64.zip" {
		t.Fatalf("selected %q, want tool-win64.zip", a.Name)
	}
}

func TestSelectAssetAmbiguousRejected(t *testing.T) {
	req := &Requirement{
		ID: "t",
		AssetRules: []AssetRule{{
			Platform:     "linux/amd64",
			IncludeRegex: "^tool.*\\.zip$",
		}},
	}
	rel := relWithAssets(
		Asset{Name: "tool-a.zip", State: "uploaded"},
		Asset{Name: "tool-b.zip", State: "uploaded"},
	)
	if _, err := SelectAsset(rel, req, "linux/amd64"); !errors.Is(err, ErrAmbiguousAssets) {
		t.Fatalf("want ambiguity error, got %v", err)
	}
}

func TestSelectAssetUnsupportedPlatform(t *testing.T) {
	req := &Requirement{
		ID: "t",
		AssetRules: []AssetRule{{
			Platform:     "windows/amd64",
			IncludeRegex: "^tool-win64\\.zip$",
		}},
	}
	rel := relWithAssets(Asset{Name: "tool-win64.zip", State: "uploaded"})
	_, err := SelectAsset(rel, req, "darwin/arm64")
	if !errors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("want platform unsupported, got %v", err)
	}
	// No asset for the supported platform is also "not available".
	_, err = SelectAsset(rel, req, "linux/amd64")
	if !errors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("want platform unsupported for linux, got %v", err)
	}
}

func TestSelectAssetIgnoresNonUploadedState(t *testing.T) {
	req := &Requirement{
		ID: "t",
		AssetRules: []AssetRule{{
			Platform:     "windows/amd64",
			IncludeRegex: "^tool\\.zip$",
		}},
	}
	rel := relWithAssets(
		Asset{Name: "tool.zip", State: "starter"},
		Asset{Name: "tool.zip", State: "uploaded"},
	)
	a, err := SelectAsset(rel, req, "windows/amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.State != "uploaded" {
		t.Fatalf("selected non-uploaded asset: %+v", a)
	}
}

func TestNormalizePlatform(t *testing.T) {
	tests := map[[2]string]string{
		{"windows", "amd64"}: "windows/amd64",
		{"linux", "amd64"}:   "linux/amd64",
		{"darwin", "arm64"}:  "darwin/arm64",
		{"linux", "x86_64"}:  "linux/amd64",
		{"android", "arm64"}: "android/arm64",
		{"ios", "arm64"}:     "ios/arm64",
		{"plan9", "amd64"}:   "",
		{"linux", "riscv64"}: "",
	}
	for in, want := range tests {
		if got := NormalizePlatform(in[0], in[1]); got != want {
			t.Fatalf("NormalizePlatform(%s/%s) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestSelectAssetFromEmbeddedCatalogRealAssets(t *testing.T) {
	cat := Embedded()
	if len(cat.Requirements) == 0 {
		t.Skip("no embedded requirement in this build")
	}
	req := &cat.Requirements[0]
	rel := relWithAssets(
		Asset{Name: "ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip", State: "uploaded", Size: 192 * 1 << 20},
		Asset{Name: "ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0.tar.xz", State: "uploaded", Size: 143 * 1 << 20},
		Asset{Name: "some-source-code.tar.gz", State: "uploaded"},
	)
	a, err := SelectAsset(rel, req, "windows/amd64")
	if err != nil {
		t.Fatalf("windows asset selection failed: %v", err)
	}
	if a.Name != "ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip" {
		t.Fatalf("wrong windows asset: %q", a.Name)
	}
	a, err = SelectAsset(rel, req, "linux/amd64")
	if err != nil {
		t.Fatalf("linux asset selection failed: %v", err)
	}
	if a.Name != "ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0.tar.xz" {
		t.Fatalf("wrong linux asset: %q", a.Name)
	}
}

func TestLooksLikeHTMLErrorPage(t *testing.T) {
	if !LooksLikeHTMLErrorPage([]byte("<!DOCTYPE html><html><body>rate limited</body></html>")) {
		t.Fatal("html page not detected")
	}
	if !LooksLikeHTMLErrorPage([]byte("\n\n<html><head><title>Oops</title></head></html>")) {
		t.Fatal("html page not detected (leading whitespace)")
	}
	if LooksLikeHTMLErrorPage([]byte("PK\x03\x04binary zip payload...")) {
		t.Fatal("binary payload misdetected as html")
	}
	if LooksLikeHTMLErrorPage([]byte("\x1f\x8b gzip payload")) {
		t.Fatal("gzip payload misdetected as html")
	}
}
