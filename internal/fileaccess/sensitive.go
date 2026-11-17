// sensitive.go — default-skip inventory for well-known secret-bearing
// locations (Section 4.4). These are skipped during folder walks even
// when the user selected the parent folder; a DIRECT selection of one
// of these files requires a warning plus a separate confirmation and
// still cannot bypass OS access controls.
package fileaccess

import (
	"path/filepath"
	"strings"
)

// sensitiveDirs are directory names (any depth, case-insensitive on
// the matching set below) whose contents are skipped wholesale.
var sensitiveDirs = map[string]bool{
	".ssh":                  true,  // SSH private keys / known_hosts
	".gnupg":                true,  // GPG keyrings
	".aws":                  true,  // AWS credentials
	".azure":                true,  // Azure CLI tokens
	".gcloud":               true,  // gcloud credentials
	".kube":                 true,  // kubeconfig tokens
	".docker":               true,  // registry credentials
	".password-store":       true,  // pass
	".thunderbird":          true,  // mail profile (passwords, mail content)
	".mozilla":              true,  // firefox profiles (logins, cookies)
	".config/google-chrome": true,  // redundant on some layouts; kept for depth-2 case below
	"AppData":               false, // NOT sensitive as a whole; handled by file names below
}

// sensitiveDirPaths are path fragments (relative, slash-separated,
// lowercase compare) skipped anywhere they appear.
var sensitiveDirPaths = []string{
	"google/chrome", "google-chrome", "chromium", "microsoft edge",
	"brave-browser", "opera software", "vivaldi",
	"filezilla", "winscp", "putty",
	"credentials", "credential store", "keyrings", "gnome-keyring", "kwallet",
	"wallets", "wallet dat",
	"mozilla/firefox",
	"internet explorer",
}

// sensitiveFileNames are file names (case-insensitive) skipped
// anywhere in a walk.
var sensitiveFileNames = map[string]bool{
	// credentials & auth
	"credentials":       true, // .aws/credentials name appears as leaf too
	".netrc":            true,
	".git-credentials":  true,
	".env":              true,
	".env.local":        true,
	"config.json":       false, // too generic; NOT listed — see note
	"authinfo":          true,
	".authinfo":         true,
	"htpasswd":          true,
	"saml-metadata.xml": false,

	// browser credential stores
	"login data":             true,
	"login data for account": true,
	"cookies":                true,
	"cookies.sqlite":         true,
	"key4.db":                true,
	"logins.json":            true,
	"cert8.db":               true,
	"key3.db":                true,
	"signedinstate":          true,
	"token":                  true,

	// key material
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	"id_rsa.pub": false, // public keys are not secrets; not skipped

	// wallets
	"wallet.dat": true, "wallet.db": true, "keystore": true,
	"seed.txt": true, "seed.json": true, "mnemonic.txt": true,

	// OS hives / vaults / dumps
	"ntuser.dat": true, "sam": true, "system": false, "security": false,
	"software":       false, // registry hive names below use extensions
	"ntuser.dat.log": true, "usrclass.dat": true,
	"config.mir":   true,
	"memory.dmp":   true,
	"hiberfil.sys": true,
	"pagefile.sys": true,
	"swapfile.sys": true,
	"core":         true, // core dumps (unix)
	"core.dump":    true,

	// password managers / secret stores
	"keepass.kdbx": true, "database.kdbx": true, "1password": true,
	"bitwarden.json": true, "vault.gpg": true, "secrets.yml": true,
	"secrets.yaml": true, "secrets.json": true, "secrets.env": true,
}

// sensitiveFileExts are extensions (lowercase, with dot) skipped
// anywhere in a walk: certificate/key containers and hive backups.
var sensitiveFileExts = map[string]bool{
	".pem":      true, // may carry private keys; skipped conservatively
	".key":      true,
	".p12":      true,
	".pfx":      true,
	".kdbx":     true,
	".ppk":      true, // PuTTY private key
	".jks":      true, // Java keystore
	".keystore": true,
	".crt":      false, // public certs are not secrets; NOT skipped
	".hiv":      true,  // registry hive backup
	".dmp":      true,  // crash dumps
	".hdmp":     true,
	".mdmp":     true,
}

// isSensitiveRelPath reports whether a walk-relative path (slash
// separated) matches the default-skip inventory. Matching is
// case-insensitive to stay safe across Windows/Unix layouts.
func isSensitiveRelPath(rel string) bool {
	if rel == "" {
		return false
	}
	lower := strings.ToLower(rel)
	parts := strings.Split(lower, "/")

	for _, p := range parts[:len(parts)-1] { // directory components
		if sensitiveDirs[p] {
			return true
		}
	}
	// Multi-component directory fragments (e.g. ".config/google-chrome").
	joined := strings.Join(parts, "/")
	for _, frag := range sensitiveDirPaths {
		if strings.Contains(joined, frag) {
			return true
		}
	}

	name := parts[len(parts)-1]
	if sensitiveFileNames[name] {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	if sensitiveFileExts[ext] {
		return true
	}
	return false
}
