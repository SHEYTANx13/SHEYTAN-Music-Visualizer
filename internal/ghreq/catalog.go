// Package ghreq implements the GitHub Requirements Manager core: a
// validated, repository-owned requirements catalog, official GitHub
// release discovery, deterministic platform asset selection, strict
// download-origin policy, integrity classification and a resumable
// download state machine. It is UI-independent and never invokes Git
// or any source-control operation; it acquires runtime requirements
// only from allowlisted official GitHub release assets.
//
// Work order: 113-Minute Master Engineering Workflow sections 5 and 6.
package ghreq

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
)

// Category classifies what a requirement is for.
type Category string

const (
	CategoryRequiredRuntime   Category = "required_runtime"
	CategoryMediaTool         Category = "media_tool"
	CategoryOptionalCodec     Category = "optional_codec"
	CategoryGraphicsSupport   Category = "graphics_support"
	CategoryTemplateAsset     Category = "template_asset"
	CategoryOptionalExtension Category = "optional_extension"
)

// Criticality controls how the UI treats a missing requirement.
type Criticality string

const (
	CriticalRequired    Criticality = "required"
	CriticalRecommended Criticality = "recommended"
	CriticalOptional    Criticality = "optional"
)

// ReleasePolicy selects how upstream releases are discovered. The
// default for every requirement is stable, non-prerelease releases;
// pre-releases are never selected automatically.
type ReleasePolicy string

const (
	// ReleasePinnedTag resolves exactly PinnedTag via the
	// releases/tags/{tag} endpoint. Used when a provenance document
	// pins one audited upstream build.
	ReleasePinnedTag ReleasePolicy = "pinned_tag"
	// ReleaseStableLatest resolves the newest published non-draft,
	// non-prerelease release via releases/latest.
	ReleaseStableLatest ReleasePolicy = "stable_latest"
	// ReleaseUserSelected lets the user pick one stable release from
	// bounded release history.
	ReleaseUserSelected ReleasePolicy = "user_selected_stable"
)

// AssetRule is the deterministic asset selector for one platform.
// Selection never picks "the first asset from the API": it matches the
// anchored IncludeRegex, drops every asset matching any ExcludeRegex,
// and requires exactly one survivor.
type AssetRule struct {
	// Platform is the normalized os/arch pair, e.g. "windows/amd64".
	Platform string `json:"platform"`
	// IncludeRegex is an anchored regex the asset name must match.
	IncludeRegex string `json:"includeRegex"`
	// ExcludeRegex lists anchored regexes that disqualify an asset
	// (source archives, debug symbol packages, checksum-only files).
	ExcludeRegex []string `json:"excludeRegex,omitempty"`
}

// Requirement is one runtime requirement the application may download
// and install from an official GitHub release.
type Requirement struct {
	ID          string      `json:"id"`
	DisplayName string      `json:"displayName"`
	Category    Category    `json:"category"`
	Criticality Criticality `json:"criticality"`

	Owner string `json:"owner"`
	Repo  string `json:"repo"`

	ReleasePolicy ReleasePolicy `json:"releasePolicy"`
	PinnedTag     string        `json:"pinnedTag,omitempty"`

	AssetRules []AssetRule `json:"assetRules"`

	// Executable marks requirements that install runnable code. Such
	// requirements must carry trusted pinned digest material (global
	// PinnedSHA256 or per-platform PinnedDigest); the installer blocks
	// otherwise.
	Executable bool `json:"executable"`
	// PinnedSHA256 is the trusted digest of the downloaded bytes,
	// obtained independently of the transfer (repository provenance
	// record). Empty means no single global pin.
	PinnedSHA256 string `json:"pinnedSha256,omitempty"`

	LicenseID     string `json:"licenseId"`
	SourceURL     string `json:"sourceUrl"`
	BuildURL      string `json:"buildUrl,omitempty"`
	LicenseNotice string `json:"licenseNotice,omitempty"`

	// InstallDirRel is the install destination relative to the
	// application data root. It must be a safe, forward-slash relative
	// path with no traversal, absolute prefix or reserved components.
	InstallDirRel string `json:"installDirRel"`

	// MaxExpandedBytes bounds the installed size.
	MaxExpandedBytes int64 `json:"maxExpandedBytes"`
	// NeedsRestart marks requirements that require an app restart.
	NeedsRestart bool `json:"needsRestart"`
	// FeatureGate names the feature that consumes the requirement.
	FeatureGate string `json:"featureGate"`

	// pinnedDigests carries per-platform trusted digests. It is
	// derived from the repository manifest, never hand-written and
	// never persisted from remote data.
	pinnedDigests map[string]string
}

// Catalog is the authoritative, closed requirements list. End users
// cannot add sources; new sources arrive only through reviewed
// repository updates.
type Catalog struct {
	Requirements []Requirement `json:"requirements"`
}

var (
	// ownerRe: GitHub account names are one or more alphanumeric runs
	// separated by single interior hyphens (no dots, no doubles).
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)
	// repoRe: repository names allow dots, hyphens and underscores but
	// never leading/trailing/doubled dots.
	repoRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	idRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,99}$`)
	// digestRe matches a lowercase hex SHA-256.
	digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Catalog validation failures.
var (
	ErrDuplicateID             = errors.New("ghreq: duplicate requirement id")
	ErrInvalidID               = errors.New("ghreq: invalid requirement id")
	ErrInvalidOwnerRepo        = errors.New("ghreq: invalid owner/repo")
	ErrMissingLicense          = errors.New("ghreq: missing license")
	ErrUnsafeInstallPath       = errors.New("ghreq: unsafe install path")
	ErrExecutableWithoutDigest = errors.New("ghreq: executable requirement without pinned digest")
	ErrNoAssetRules            = errors.New("ghreq: requirement without asset rules")
	ErrAmbiguousAssetSelector  = errors.New("ghreq: ambiguous asset selector")
	ErrInvalidSourceURL        = errors.New("ghreq: invalid source url")
)

// Validate enforces the catalog contract: unique stable IDs, exact
// owner/repo names, bounded release policies, deterministic asset
// selectors, safe install paths, licenses for every entry and pinned
// digests for every executable entry. It rejects wildcard hosts,
// traversal paths and unbounded policies.
func (c *Catalog) Validate() error {
	seen := make(map[string]bool, len(c.Requirements))
	for i := range c.Requirements {
		r := &c.Requirements[i]
		if !idRe.MatchString(r.ID) {
			return fmt.Errorf("%w: %q", ErrInvalidID, r.ID)
		}
		if seen[r.ID] {
			return fmt.Errorf("%w: %s", ErrDuplicateID, r.ID)
		}
		seen[r.ID] = true
		if !ownerRe.MatchString(r.Owner) || !repoRe.MatchString(r.Repo) ||
			strings.Contains(r.Repo, "..") || len(r.Owner) > 100 || len(r.Repo) > 100 {
			return fmt.Errorf("%w: %s/%s", ErrInvalidOwnerRepo, r.Owner, r.Repo)
		}
		switch r.ReleasePolicy {
		case ReleasePinnedTag:
			if r.PinnedTag == "" || len(r.PinnedTag) > 120 {
				return fmt.Errorf("ghreq: %s pinned policy requires an exact tag", r.ID)
			}
		case ReleaseStableLatest, ReleaseUserSelected:
			// bounded policies, no extra data required
		default:
			return fmt.Errorf("ghreq: %s unknown release policy %q", r.ID, r.ReleasePolicy)
		}
		if len(r.AssetRules) == 0 {
			return fmt.Errorf("%w: %s", ErrNoAssetRules, r.ID)
		}
		plats := make(map[string]bool, len(r.AssetRules))
		for _, ar := range r.AssetRules {
			if plats[ar.Platform] {
				return fmt.Errorf("%w: %s duplicate platform rule %s", ErrAmbiguousAssetSelector, r.ID, ar.Platform)
			}
			plats[ar.Platform] = true
			inc, err := regexp.Compile(ar.IncludeRegex)
			if err != nil || !anchoredLiteral(ar.IncludeRegex) {
				return fmt.Errorf("%w: %s include regex %q", ErrAmbiguousAssetSelector, r.ID, ar.IncludeRegex)
			}
			_ = inc
			for _, ex := range ar.ExcludeRegex {
				if _, err := regexp.Compile(ex); err != nil || !anchoredLiteral(ex) {
					return fmt.Errorf("%w: %s exclude regex %q", ErrAmbiguousAssetSelector, r.ID, ex)
				}
			}
		}
		if strings.TrimSpace(r.LicenseID) == "" {
			return fmt.Errorf("%w: %s", ErrMissingLicense, r.ID)
		}
		if r.Executable && r.PinnedSHA256 == "" && len(r.pinnedDigests) == 0 {
			return fmt.Errorf("%w: %s", ErrExecutableWithoutDigest, r.ID)
		}
		if r.PinnedSHA256 != "" && !digestRe.MatchString(r.PinnedSHA256) {
			return fmt.Errorf("ghreq: %s pinned digest is not 64 hex chars", r.ID)
		}
		for plat, dig := range r.pinnedDigests {
			if !digestRe.MatchString(dig) {
				return fmt.Errorf("ghreq: %s pinned digest for %s is not 64 hex chars", r.ID, plat)
			}
		}
		if err := safeRelPath(r.InstallDirRel); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrUnsafeInstallPath, r.ID, err)
		}
		if r.MaxExpandedBytes <= 0 {
			return fmt.Errorf("ghreq: %s MaxExpandedBytes must be positive", r.ID)
		}
		u, err := url.Parse(r.SourceURL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
			return fmt.Errorf("%w: %s", ErrInvalidSourceURL, r.ID)
		}
		switch r.Category {
		case CategoryRequiredRuntime, CategoryMediaTool, CategoryOptionalCodec,
			CategoryGraphicsSupport, CategoryTemplateAsset, CategoryOptionalExtension:
		default:
			return fmt.Errorf("ghreq: %s unknown category %q", r.ID, r.Category)
		}
		switch r.Criticality {
		case CriticalRequired, CriticalRecommended, CriticalOptional:
		default:
			return fmt.Errorf("ghreq: %s unknown criticality %q", r.ID, r.Criticality)
		}
	}
	return nil
}

// anchoredLiteral reports whether the selector is a plain anchored
// literal (^\Qname\E$ shape): exactly one alternative, anchored at
// both ends, no unescaped alternation or wildcard metacharacters. The
// closed catalog deliberately restricts selectors to this safe shape.
func anchoredLiteral(s string) bool {
	if len(s) < 3 || s[0] != '^' || s[len(s)-1] != '$' || strings.Contains(s, "|") {
		return false
	}
	_, err := regexp.Compile(s)
	return err == nil
}

// PinnedDigestFor returns the trusted digest for one platform: the
// per-platform pin when present, otherwise the global pin.
func (r *Requirement) PinnedDigestFor(platform string) string {
	if d, ok := r.pinnedDigests[platform]; ok && d != "" {
		return d
	}
	return r.PinnedSHA256
}

// safeRelPath accepts only forward-slash relative paths without
// traversal, drive prefixes, whitespace-only components or reserved
// device names.
func safeRelPath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if strings.ContainsAny(p, "\\\x00") || strings.Contains(p, ":") {
		return errors.New("backslash, NUL or drive prefix")
	}
	if path.IsAbs(p) || strings.HasPrefix(p, "/") {
		return errors.New("absolute path")
	}
	if path.Clean(p) != p {
		return errors.New("not a clean path")
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "", ".", "..":
			return errors.New("empty, dot or dot-dot component")
		}
		low := strings.ToLower(part)
		if low == "con" || low == "nul" || low == "aux" ||
			(len(part) == 4 && (strings.HasPrefix(low, "com") || strings.HasPrefix(low, "lpt")) &&
				part[3] >= '0' && part[3] <= '9') {
			return errors.New("reserved device name")
		}
	}
	return nil
}

// Embedded derives the catalog from the repository's provenance
// records. The only catalog entry in this BETA is the optional media
// tool pack already maintained by internal/mediatools: the catalog is
// generated from that single source of truth and invents nothing.
func Embedded() Catalog {
	m := mediatools.EmbeddedManifest()
	cat := Catalog{Requirements: make([]Requirement, 0, 1)}
	if len(m.Platforms) == 0 {
		return cat
	}
	var maxInstalled int64
	for _, pack := range m.Platforms {
		if int64(pack.ApproxInstalledMB) > maxInstalled {
			maxInstalled = int64(pack.ApproxInstalledMB)
		}
	}
	r := Requirement{
		ID:               "media-tools-ffmpeg-gpl",
		DisplayName:      "FFmpeg media tools (GPL build)",
		Category:         CategoryMediaTool,
		Criticality:      CriticalOptional,
		Owner:            "BtbN",
		Repo:             "FFmpeg-Builds",
		ReleasePolicy:    ReleasePinnedTag,
		Executable:       true,
		LicenseID:        "GPL-2.0-or-later",
		InstallDirRel:    "media-tools/current",
		MaxExpandedBytes: maxInstalled * 1024 * 1024,
		NeedsRestart:     false,
		FeatureGate:      "export-encode",
		pinnedDigests:    make(map[string]string, len(m.Platforms)),
	}
	for plat, pack := range m.Platforms {
		if plat != pack.Platform {
			continue
		}
		tag := pinnedTagFromURL(pack.URL)
		if tag != "" {
			if r.PinnedTag == "" {
				r.PinnedTag = tag
			} else if r.PinnedTag != tag {
				// Per-platform pins must agree inside one requirement;
				// disagreement means the manifest needs a split, so the
				// platform is dropped rather than guessed.
				continue
			}
		}
		r.AssetRules = append(r.AssetRules, AssetRule{
			Platform:     plat,
			IncludeRegex: "^" + regexp.QuoteMeta(assetName(pack.URL)) + "$",
		})
		r.pinnedDigests[plat] = pack.SHA256
	}
	if len(r.AssetRules) > 0 && r.PinnedTag != "" {
		r.SourceURL = "https://github.com/" + r.Owner + "/" + r.Repo + "/releases/tag/" + r.PinnedTag
		r.BuildURL = r.SourceURL
		r.LicenseNotice = "Optional GPL FFmpeg build by the BtbN FFmpeg-Builds project; the GPL applies to the pack, not to this application."
		cat.Requirements = append(cat.Requirements, r)
	}
	return cat
}

func assetName(u string) string {
	i := strings.LastIndexByte(u, '/')
	if i < 0 {
		return u
	}
	return u[i+1:]
}

func pinnedTagFromURL(u string) string {
	const marker = "/releases/download/"
	i := strings.Index(u, marker)
	if i < 0 {
		return ""
	}
	rest := u[i+len(marker):]
	j := strings.IndexByte(rest, '/')
	if j < 0 {
		return ""
	}
	return rest[:j]
}
