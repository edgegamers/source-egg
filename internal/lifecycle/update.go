// Package lifecycle checks and installs verified node releases. It never edits
// configuration, server volumes, shared game installations, or private state.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

const MaxAsset = 128 << 20

var ErrUpToDate = errors.New("node is up to date")
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var servicePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.service)?$`)
var versionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type Config struct {
	Enabled      bool   `json:"enabled"`
	Channel      string `json:"channel"`
	Release      string `json:"release,omitempty"`
	CheckMinutes int    `json:"check_minutes"`
}

func DefaultConfig() Config { return Config{Channel: "stable", CheckMinutes: 360} }
func (c Config) Validate() error {
	if c.Channel != "" && c.Channel != "stable" && c.Channel != "beta" && c.Channel != "dev" {
		return errors.New("node update channel must be stable, beta, or dev")
	}
	if c.CheckMinutes < 0 || c.CheckMinutes > 525600 {
		return errors.New("node update check_minutes must be between 0 and 525600")
	}
	if c.Release != "" {
		if _, ok := parseVersion(c.Release); !ok || !strings.HasPrefix(c.Release, "v") {
			return errors.New("node release must be an exact tag such as v1.2.3")
		}
	}
	return nil
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}
type Candidate struct {
	Version           string
	Binary, Checksums Asset
}
type Result struct {
	Version   string
	Updated   bool
	Err       error
	CheckedAt time.Time
}
type Options struct {
	Client                                                  *http.Client
	Repository, AssetName, APIBase, BinaryPath, ServiceName string
	Run                                                     func(context.Context, string, ...string) error
	// BeforeRestart persists notices after the verified swap and before the
	// service request. An error aborts the restart and restores the old binary.
	BeforeRestart func(context.Context, string) error
	Now           func() time.Time
	After         func(time.Duration) <-chan time.Time
}
type Manager struct {
	cfg     Config
	options Options
	mu      sync.Mutex
}

func New(c Config, o Options) (*Manager, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if c.Channel == "" {
		c.Channel = "stable"
	}
	if c.CheckMinutes == 0 {
		c.CheckMinutes = 360
	}
	if o.Repository == "" {
		o.Repository = "edgegamers/source-egg"
	}
	if !repositoryPattern.MatchString(o.Repository) {
		return nil, errors.New("invalid release repository")
	}
	if o.AssetName == "" {
		o.AssetName = "sourcenode_linux_amd64"
	}
	if filepath.Base(o.AssetName) != o.AssetName || strings.ContainsAny(o.AssetName, "/\\?#\r\n") || o.AssetName == "." {
		return nil, errors.New("invalid binary asset name")
	}
	if o.APIBase == "" {
		o.APIBase = "https://api.github.com"
	}
	o.APIBase = strings.TrimRight(o.APIBase, "/")
	u, e := url.Parse(o.APIBase)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid release API base")
	}
	if o.ServiceName == "" {
		o.ServiceName = "sourcenode"
	}
	if !servicePattern.MatchString(o.ServiceName) {
		return nil, errors.New("invalid node service name")
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	client := *o.Client
	if client.Timeout == 0 {
		client.Timeout = 5 * time.Minute
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !officialURL(req.URL.String()) {
			return errors.New("unapproved release redirect")
		}
		return nil
	}
	o.Client = &client
	if o.Run == nil {
		o.Run = func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.After == nil {
		o.After = time.After
	}
	return &Manager{cfg: c, options: o}, nil
}
func officialURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	}
	return false
}

type release struct {
	Tag    string  `json:"tag_name"`
	Draft  bool    `json:"draft"`
	Pre    bool    `json:"prerelease"`
	Assets []Asset `json:"assets"`
}

func (m *Manager) Check(ctx context.Context, current string) (*Candidate, error) {
	if m.cfg.Channel == "dev" && m.cfg.Release == "" {
		return nil, ErrUpToDate
	}
	path := m.options.APIBase + "/repos/" + m.options.Repository + "/releases?per_page=100"
	if m.cfg.Release != "" {
		path = m.options.APIBase + "/repos/" + m.options.Repository + "/releases/tags/" + url.PathEscape(m.cfg.Release)
	}
	raw, e := m.get(ctx, path, 8<<20, false)
	if e != nil {
		return nil, fmt.Errorf("node release lookup: %w", e)
	}
	var all []release
	if m.cfg.Release != "" {
		var r release
		if e = json.Unmarshal(raw, &r); e == nil {
			all = []release{r}
		}
	} else {
		e = json.Unmarshal(raw, &all)
	}
	if e != nil {
		return nil, errors.New("invalid node release response")
	}
	sort.SliceStable(all, func(i, j int) bool { return compareVersion(all[i].Tag, all[j].Tag) > 0 })
	for _, r := range all {
		v, ok := parseVersion(r.Tag)
		if !ok || r.Draft {
			continue
		}
		if m.cfg.Release != "" {
			if r.Tag != m.cfg.Release {
				continue
			}
		} else {
			if m.cfg.Channel == "stable" && (r.Pre || v.pre != "") {
				continue
			}
			if _, ok := parseVersion(current); !ok || compareVersion(r.Tag, current) <= 0 {
				return nil, ErrUpToDate
			}
		}
		c := &Candidate{Version: r.Tag}
		for _, a := range r.Assets {
			if a.Name == m.options.AssetName {
				if c.Binary.Name != "" {
					return nil, errors.New("duplicate binary release asset")
				}
				c.Binary = a
			}
			if a.Name == "checksums.txt" {
				if c.Checksums.Name == "checksums.txt" {
					return nil, errors.New("duplicate checksum release asset")
				}
				c.Checksums = a
			}
		}
		if c.Checksums.Name == "" {
			for _, a := range r.Assets {
				if a.Name == m.options.AssetName+".sha256" {
					if c.Checksums.Name != "" {
						return nil, errors.New("duplicate checksum release asset")
					}
					c.Checksums = a
				}
			}
		}
		if e = m.validateCandidate(c); e != nil {
			return nil, e
		}
		if m.cfg.Release != "" && compareVersion(r.Tag, current) == 0 {
			return nil, ErrUpToDate
		}
		return c, nil
	}
	return nil, errors.New("no matching node release")
}
func (m *Manager) validateCandidate(c *Candidate) error {
	if c == nil {
		return errors.New("missing node update candidate")
	}
	if _, ok := parseVersion(c.Version); !ok {
		return errors.New("invalid node release version")
	}
	if c.Binary.Name != m.options.AssetName || (c.Checksums.Name != "checksums.txt" && c.Checksums.Name != m.options.AssetName+".sha256") {
		return errors.New("node release is missing binary or checksums")
	}
	for _, a := range []Asset{c.Binary, c.Checksums} {
		want := "https://github.com/" + m.options.Repository + "/releases/download/" + c.Version + "/" + a.Name
		if a.URL != want || !officialURL(a.URL) {
			return errors.New("node release asset must come from its exact GitHub repository and tag")
		}
	}
	return nil
}
func (m *Manager) get(ctx context.Context, raw string, limit int64, asset bool) ([]byte, error) {
	if asset && !officialURL(raw) {
		return nil, errors.New("unapproved release asset URL")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "sourcenode")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, e := m.options.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("release response is empty or exceeds size limit")
	}
	return data, nil
}

// Apply verifies both assets before replacing the binary atomically. The old
// binary remains at .prev. Failure to ask systemd to restart restores it.
func (m *Manager) Apply(ctx context.Context, c *Candidate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.validateCandidate(c); e != nil {
		return e
	}
	path := m.options.BinaryPath
	if e := regularTarget(path); e != nil {
		return e
	}
	// Pin the validated parent before HTTP or injected callbacks can change its
	// pathname. Every subsequent write uses this same root handle.
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer root.Close()
	sums, e := m.get(ctx, c.Checksums.URL, 1<<20, true)
	if e != nil {
		return fmt.Errorf("download node checksums: %w", e)
	}
	want, e := expectedSum(sums, m.options.AssetName)
	if e != nil {
		return e
	}
	name := filepath.Base(path)
	temp := ".sourcenode-new-" + fsx.Token()
	defer root.Remove(temp)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.Binary.URL, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "sourcenode")
	resp, e := m.options.Client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node binary HTTP %d", resp.StatusCode)
	}
	out, e := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if e != nil {
		return e
	}
	if e = out.Chmod(0755); e != nil {
		out.Close()
		return e
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, hash), io.LimitReader(resp.Body, MaxAsset+1))
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if n == 0 || n > MaxAsset {
		return errors.New("node binary is empty or exceeds size limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return errors.New("node binary checksum mismatch")
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = copyWithin(root, name, name+".prev"); e != nil {
		return fmt.Errorf("save previous node binary: %w", e)
	}
	if e = root.Rename(temp, name); e != nil {
		return e
	}
	rollback := func(cause error) error {
		if e := copyWithin(root, name+".prev", name); e != nil {
			return errors.Join(cause, fmt.Errorf("restore previous node binary: %w", e))
		}
		return errors.Join(cause, syncRoot(root))
	}
	if e = syncRoot(root); e != nil {
		return rollback(e)
	}
	if m.options.BeforeRestart != nil {
		if e = m.options.BeforeRestart(ctx, c.Version); e != nil {
			return rollback(fmt.Errorf("prepare node restart: %w", e))
		}
	}
	// A daemon cannot synchronously wait for systemd to stop that same daemon.
	if e = m.options.Run(ctx, "systemctl", "--no-block", "try-restart", m.options.ServiceName); e != nil {
		return rollback(fmt.Errorf("restart updated node: %w", e))
	}
	return nil
}
func expectedSum(raw []byte, name string) (string, error) {
	var sum string
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.TrimPrefix(f[1], "*") != name {
			continue
		}
		if len(f) != 2 || sum != "" || len(f[0]) != 64 {
			return "", errors.New("invalid or duplicate binary checksum")
		}
		b, e := hex.DecodeString(f[0])
		if e != nil || len(b) != 32 {
			return "", errors.New("invalid binary checksum")
		}
		sum = strings.ToLower(f[0])
	}
	if sum == "" {
		return "", errors.New("checksums have no binary entry")
	}
	return sum, nil
}
func copyWithin(root *os.Root, src, dest string) error {
	before, e := root.Lstat(src)
	if e != nil {
		return e
	}
	if !before.Mode().IsRegular() {
		return errors.New("binary source changed to a nonregular file")
	}
	in, e := root.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return errors.New("binary source changed during open")
	}
	temp := ".sourcenode-copy-" + fsx.Token()
	defer root.Remove(temp)
	out, e := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if e != nil {
		return e
	}
	if e = out.Chmod(0755); e != nil {
		out.Close()
		return e
	}
	_, e = io.Copy(out, in)
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return root.Rename(temp, dest)
}
func syncRoot(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func regularTarget(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("node binary must be an explicit absolute file path")
	}
	path = filepath.Clean(path)
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("node binary path contains a symlink")
		}
		if p == path && !info.Mode().IsRegular() {
			return errors.New("node binary is not a regular file")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// Run owns the optional update loop. Manual Check and Apply remain available
// when automatic updating is disabled. Each successful update advances current.
func (m *Manager) Run(ctx context.Context, current string, report func(Result)) {
	if !m.cfg.Enabled {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.options.After(time.Duration(m.cfg.CheckMinutes) * time.Minute):
		}
		checkedAt := m.options.Now().UTC()
		c, e := m.Check(ctx, current)
		if errors.Is(e, ErrUpToDate) {
			continue
		}
		r := Result{Err: e, CheckedAt: checkedAt}
		if e == nil {
			r.Version = c.Version
			r.Err = m.Apply(ctx, c)
			if r.Err == nil {
				r.Updated = true
				current = c.Version
			}
		}
		if report != nil {
			report(r)
		}
	}
}

type semver struct {
	parts [3]uint64
	pre   string
}

func parseVersion(raw string) (semver, bool) {
	var v semver
	f := versionPattern.FindStringSubmatch(raw)
	if f == nil {
		return v, false
	}
	for i := range v.parts {
		if len(f[i+1]) > 1 && f[i+1][0] == '0' {
			return v, false
		}
		n, e := strconv.ParseUint(f[i+1], 10, 64)
		if e != nil {
			return v, false
		}
		v.parts[i] = n
	}
	v.pre = f[4]
	for _, p := range strings.Split(v.pre, ".") {
		if v.pre != "" && p == "" {
			return v, false
		}
	}
	return v, true
}
func compareVersion(a, b string) int {
	x, ok := parseVersion(a)
	if !ok {
		return -1
	}
	y, ok := parseVersion(b)
	if !ok {
		return 1
	}
	for i := range x.parts {
		if x.parts[i] > y.parts[i] {
			return 1
		}
		if x.parts[i] < y.parts[i] {
			return -1
		}
	}
	if x.pre == y.pre {
		return 0
	}
	if x.pre == "" {
		return 1
	}
	if y.pre == "" {
		return -1
	}
	xx, yy := strings.Split(x.pre, "."), strings.Split(y.pre, ".")
	for i := 0; i < len(xx) && i < len(yy); i++ {
		if xx[i] == yy[i] {
			continue
		}
		nx, ex := strconv.ParseUint(xx[i], 10, 64)
		ny, ey := strconv.ParseUint(yy[i], 10, 64)
		if ex == nil && ey == nil {
			if nx > ny {
				return 1
			}
			return -1
		}
		if ex == nil {
			return -1
		}
		if ey == nil {
			return 1
		}
		if xx[i] > yy[i] {
			return 1
		}
		return -1
	}
	if len(xx) > len(yy) {
		return 1
	}
	return -1
}
