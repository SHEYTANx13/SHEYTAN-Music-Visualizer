package mediatools

// manifest_test.go — manifest contract fixtures (run 43712071111): the
// committed document must validate, use HTTPS pinned URLs (no floating
// "latest"), carry 64-hex digests, and expose exactly the supported
// platforms.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbeddedManifestIsValid(t *testing.T) {
	m := EmbeddedManifest()
	if m.Schema != SchemaVersion {
		t.Fatalf("schema = %d, want %d", m.Schema, SchemaVersion)
	}
	for _, key := range []string{"windows/amd64", "linux/amd64"} {
		p, ok := m.Platforms[key]
		if !ok {
			t.Fatalf("manifest missing platform %q (have %v)", key, supportedPlatforms(m))
		}
		if !strings.HasPrefix(p.URL, "https://") {
			t.Errorf("%s: URL not HTTPS: %s", key, p.URL)
		}
		if strings.Contains(p.URL, "latest") {
			t.Errorf("%s: floating latest URL forbidden", key)
		}
		if len(p.SHA256) != 64 {
			t.Errorf("%s: digest length = %d", key, len(p.SHA256))
		}
		if p.Version == "" || p.LicenseClass == "" || p.SourceURL == "" {
			t.Errorf("%s: identity/license fields empty", key)
		}
		found := false
		for _, e := range p.RequiredEncoders {
			if e == "libx265" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: required encoders must include libx265 (HEVC promise)", key)
		}
	}
}

func TestParseManifestRejectsBadDocuments(t *testing.T) {
	cases := map[string]string{
		"bad json":      `{`,
		"wrong schema":  `{"schema": 99, "platforms": {}}`,
		"no platforms":  `{"schema": 1, "platforms": {}}`,
		"http url":      `{"schema":1,"platforms":{"windows/amd64":{"platform":"windows/amd64","version":"x","url":"http://example.com/a.zip","sha256":"` + strings.Repeat("a", 64) + `","archiveFormat":"zip","ffmpegPath":"a/bin/ffmpeg.exe","ffprobePath":"a/bin/ffprobe.exe","requiredEncoders":["libx264"],"licenseClass":"GPL","licenseNotice":"n","sourceUrl":"https://x"}}}`,
		"latest url":    `{"schema":1,"platforms":{"windows/amd64":{"platform":"windows/amd64","version":"x","url":"https://example.com/latest.zip","sha256":"` + strings.Repeat("a", 64) + `","archiveFormat":"zip","ffmpegPath":"a/bin/ffmpeg.exe","ffprobePath":"a/bin/ffprobe.exe","requiredEncoders":["libx264"],"licenseClass":"GPL","licenseNotice":"n","sourceUrl":"https://x"}}}`,
		"short digest":  `{"schema":1,"platforms":{"windows/amd64":{"platform":"windows/amd64","version":"x","url":"https://example.com/a.zip","sha256":"abcd","archiveFormat":"zip","ffmpegPath":"a/bin/ffmpeg.exe","ffprobePath":"a/bin/ffprobe.exe","requiredEncoders":["libx264"],"licenseClass":"GPL","licenseNotice":"n","sourceUrl":"https://x"}}}`,
		"traversal exe": `{"schema":1,"platforms":{"windows/amd64":{"platform":"windows/amd64","version":"x","url":"https://example.com/a.zip","sha256":"` + strings.Repeat("a", 64) + `","archiveFormat":"zip","ffmpegPath":"../bin/ffmpeg.exe","ffprobePath":"a/bin/ffprobe.exe","requiredEncoders":["libx264"],"licenseClass":"GPL","licenseNotice":"n","sourceUrl":"https://x"}}}`,
	}
	for name, doc := range cases {
		if _, err := ParseManifest([]byte(doc)); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestPackForUnsupportedPlatform(t *testing.T) {
	m := EmbeddedManifest()
	if _, err := PackFor(m, "plan9", "arm"); err == nil {
		t.Fatal("expected unsupported-platform error")
	} else if !strings.Contains(err.Error(), "plan9/arm") {
		t.Fatalf("error must name the platform: %v", err)
	}
	if p, err := PackFor(m, "windows", "amd64"); err != nil {
		t.Fatalf("windows/amd64 must resolve: %v", err)
	} else if p.Platform != "windows/amd64" {
		t.Fatalf("wrong platform: %s", p.Platform)
	}
}

func TestInstalledManifestRoundTrip(t *testing.T) {
	p := EmbeddedManifest().Platforms["linux/amd64"]
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back PlatformPack
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Version != p.Version || back.SHA256 != p.SHA256 {
		t.Fatal("pack identity lost in JSON round trip")
	}
}
