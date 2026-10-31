// Package plugins manages server-owned release packages independently of node frameworks.
package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

type Manifest struct {
	Version            int      `json:"version"`
	CheckIntervalHours int      `json:"check_interval_hours"`
	AutoApprove        bool     `json:"auto_approve,omitempty"`
	Plugins            []Plugin `json:"plugins"`
}
type Plugin struct {
	ID          string    `json:"id"`
	Source      Source    `json:"source"`
	Asset       string    `json:"asset,omitempty"`
	Format      string    `json:"format,omitempty"`
	Mappings    []Mapping `json:"mappings"`
	Preserve    []string  `json:"preserve,omitempty"`
	Pin         string    `json:"pin,omitempty"`
	Prerelease  bool      `json:"prerelease,omitempty"`
	Build       *Build    `json:"build,omitempty"`
	AutoApprove *bool     `json:"auto_approve,omitempty"`
}
type Source struct {
	Provider   string `json:"provider"`
	Repository string `json:"repository"`
	URL        string `json:"url,omitempty"`
	Credential string `json:"credential,omitempty"`
	Ref        string `json:"ref,omitempty"`
}
type Build struct {
	SourceMod    string            `json:"sourcemod"`
	Sources      []string          `json:"sources"`
	IncludeDirs  []string          `json:"include_dirs,omitempty"`
	Dependencies []BuildDependency `json:"dependencies,omitempty"`
}
type BuildDependency struct {
	Source      Source   `json:"source"`
	IncludeDirs []string `json:"include_dirs"`
}
type Mapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
}
type Release struct {
	Version      string     `json:"version"`
	ID           string     `json:"id"`
	Asset        Asset      `json:"asset"`
	Dependencies []Revision `json:"dependencies,omitempty"`
}
type Revision struct {
	Commit string `json:"commit"`
	Asset  Asset  `json:"asset"`
}
type File struct {
	SHA256 string `json:"sha256"`
	Seed   bool   `json:"seed,omitempty"`
}
type Snapshot struct {
	ID        string          `json:"id"`
	Version   string          `json:"version"`
	Recipe    string          `json:"recipe"`
	Directory string          `json:"directory"`
	Files     map[string]File `json:"files"`
}
type Installation struct {
	Current  Snapshot   `json:"current"`
	Previous []Snapshot `json:"previous,omitempty"`
}
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}
type ReleaseSource interface {
	Resolve(context.Context, Plugin) (Release, error)
	Download(context.Context, Plugin, Release, string) error
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func cleanRelative(s string, dot bool) bool {
	if s == "." {
		return dot
	}
	if s == "" || strings.ContainsAny(s, "\\\x00\r\n\t") || strings.HasPrefix(s, "/") || path.Clean(s) != s {
		return false
	}
	for _, c := range strings.Split(s, "/") {
		if c == ".." || c == "." || c == "" {
			return false
		}
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

// ValidTarget permits plugin payload areas, never server binaries or private manager state.
func ValidTarget(s string) bool {
	if !cleanRelative(s, false) {
		return false
	}
	root, _, _ := strings.Cut(s, "/")
	switch root {
	case "addons", "cfg", "scripts", "materials", "models", "sound", "resource", "maps":
		return true
	}
	return false
}
func Protected(s string, preserve []string) bool {
	roots := []string{"cfg", "addons/sourcemod/configs", "addons/sourcemod/data", "addons/sourcemod/logs", "addons/metamod.vdf"}
	roots = append(roots, preserve...)
	for _, root := range roots {
		if s == root || strings.HasPrefix(s, root+"/") {
			return true
		}
	}
	return false
}
func ValidatePlugin(p Plugin) error {
	if !identifier.MatchString(p.ID) {
		return fmt.Errorf("invalid plugin id")
	}
	if err := validateSource(p.Source); err != nil {
		return fmt.Errorf("%s: %w", p.ID, err)
	}
	if p.Build != nil {
		if p.Asset != "" || p.Format != "" || p.Pin != "" || p.Prerelease {
			return fmt.Errorf("%s: source builds cannot select release assets, formats, pins or prereleases; use source.ref", p.ID)
		}
		if err := validateBuild(*p.Build); err != nil {
			return fmt.Errorf("%s: %w", p.ID, err)
		}
	} else {
		if p.Source.Ref != "" {
			return fmt.Errorf("%s: source.ref requires a build recipe", p.ID)
		}
		if p.Asset == "" || strings.ContainsAny(p.Asset, "/\\\x00\r\n") {
			return fmt.Errorf("%s: asset must select a filename", p.ID)
		}
		if _, e := path.Match(p.Asset, "probe"); e != nil {
			return fmt.Errorf("%s: invalid asset pattern", p.ID)
		}
		switch p.Format {
		case "", "auto", "zip", "tar.gz", "raw":
		default:
			return fmt.Errorf("%s: unsupported package format", p.ID)
		}
	}
	if len(p.Mappings) == 0 || len(p.Mappings) > 100 {
		return fmt.Errorf("%s: provide 1–100 file mappings", p.ID)
	}
	for _, m := range p.Mappings {
		if !cleanRelative(m.From, true) || !(m.To == "." || ValidTarget(m.To)) {
			return fmt.Errorf("%s: unsafe file mapping", p.ID)
		}
	}
	for _, s := range p.Preserve {
		if !ValidTarget(s) {
			return fmt.Errorf("%s: invalid preserve path", p.ID)
		}
	}
	if len(p.Pin) > 200 || strings.ContainsAny(p.Pin, "\x00\r\n") {
		return fmt.Errorf("%s: invalid version pin", p.ID)
	}
	return nil
}

func validateSource(s Source) error {
	if s.Provider != "github" && s.Provider != "gitlab" {
		return fmt.Errorf("provider must be github or gitlab")
	}
	parts := strings.Split(s.Repository, "/")
	if len(parts) < 2 || (s.Provider == "github" && len(parts) != 2) {
		return fmt.Errorf("repository must be an owner/project path")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, " ?#%\\:@\r\n\t") {
			return fmt.Errorf("invalid repository path")
		}
	}
	if s.URL != "" {
		u, e := url.Parse(s.URL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid provider URL")
		}
		local := u.Hostname() == "localhost"
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			local = ip.IsLoopback()
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return fmt.Errorf("provider requires HTTPS")
		}
	}
	if s.Credential != "" && (!identifier.MatchString(s.Credential) || s.Provider != "gitlab") {
		return fmt.Errorf("credentials require a named GitLab credential")
	}
	if len(s.Ref) > 200 || strings.HasPrefix(s.Ref, "/") || strings.HasSuffix(s.Ref, "/") || strings.HasPrefix(s.Ref, "-") || strings.Contains(s.Ref, "..") || strings.Contains(s.Ref, "@{") || strings.ContainsAny(s.Ref, " \\~^:?*[\x00\r\n\t") {
		return fmt.Errorf("invalid repository ref")
	}
	return nil
}
func (m *Manifest) Validate() error {
	if m.Version != 1 {
		return fmt.Errorf("plugin manifest version must be 1")
	}
	if m.CheckIntervalHours == 0 {
		m.CheckIntervalHours = 6
	}
	if m.CheckIntervalHours < 1 || m.CheckIntervalHours > 8760 {
		return fmt.Errorf("check_interval_hours must be 1–8760")
	}
	if len(m.Plugins) > 100 {
		return fmt.Errorf("at most 100 plugins are supported")
	}
	seen := map[string]bool{}
	for _, p := range m.Plugins {
		if err := ValidatePlugin(p); err != nil {
			return err
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate plugin id: %s", p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}
func recipeHash(p Plugin) string {
	// Approval policy controls deployment, not the staged package's contents.
	p.AutoApprove = nil
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
