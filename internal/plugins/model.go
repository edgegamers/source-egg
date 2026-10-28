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
	Plugins            []Plugin `json:"plugins"`
}
type Plugin struct {
	ID         string    `json:"id"`
	Source     Source    `json:"source"`
	Asset      string    `json:"asset"`
	Format     string    `json:"format,omitempty"`
	Mappings   []Mapping `json:"mappings"`
	Preserve   []string  `json:"preserve,omitempty"`
	Pin        string    `json:"pin,omitempty"`
	Prerelease bool      `json:"prerelease,omitempty"`
}
type Source struct {
	Provider   string `json:"provider"`
	Repository string `json:"repository"`
	URL        string `json:"url,omitempty"`
	Credential string `json:"credential,omitempty"`
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
	Version string `json:"version"`
	ID      string `json:"id"`
	Asset   Asset  `json:"asset"`
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
	if p.Source.Provider != "github" && p.Source.Provider != "gitlab" {
		return fmt.Errorf("%s: provider must be github or gitlab", p.ID)
	}
	parts := strings.Split(p.Source.Repository, "/")
	if len(parts) < 2 || (p.Source.Provider == "github" && len(parts) != 2) {
		return fmt.Errorf("%s: repository must be an owner/project path", p.ID)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, " ?#%\\:@\r\n\t") {
			return fmt.Errorf("%s: invalid repository path", p.ID)
		}
	}
	if p.Source.URL != "" {
		u, e := url.Parse(p.Source.URL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s: invalid provider URL", p.ID)
		}
		local := u.Hostname() == "localhost"
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			local = ip.IsLoopback()
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return fmt.Errorf("%s: provider requires HTTPS", p.ID)
		}
	}
	if p.Source.Credential != "" && (!identifier.MatchString(p.Source.Credential) || p.Source.Provider != "gitlab") {
		return fmt.Errorf("%s: credentials require a named GitLab credential", p.ID)
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
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
