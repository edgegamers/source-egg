package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
)

type Server struct {
	ID, UUID, Volume, Boot string
	PID                    int
	Running                bool
	ExitCode               int
	OOMKilled              bool
	Identity               game.Identity
	Env                    map[string]string
}
type dockerInspect struct {
	ID     string `json:"Id"`
	Name   string
	Config struct {
		Image string
		Env   []string
	}
	State struct {
		Running   bool
		Pid       int
		StartedAt string
		ExitCode  int
		OOMKilled bool
	}
	Mounts []struct{ Destination, Source string }
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func imageRepo(image string) string {
	s := strings.SplitN(image, "@", 2)[0]
	if i := strings.LastIndex(s, ":"); i > strings.LastIndex(s, "/") {
		s = s[:i]
	}
	return s
}
func serverFrom(c dockerInspect, images []string) (Server, error) {
	allowed := false
	for _, image := range images {
		if imageRepo(c.Config.Image) == image {
			allowed = true
		}
	}
	if !allowed {
		return Server{}, errors.New("unmanaged image")
	}
	s := Server{ID: c.ID, UUID: strings.TrimPrefix(c.Name, "/"), Boot: c.State.StartedAt, PID: c.State.Pid, Running: c.State.Running, Env: map[string]string{}}
	s.ExitCode = c.State.ExitCode
	s.OOMKilled = c.State.OOMKilled
	if !validID.MatchString(s.UUID) {
		return Server{}, errors.New("invalid server UUID")
	}
	for _, pair := range c.Config.Env {
		p := strings.SplitN(pair, "=", 2)
		if len(p) == 2 {
			s.Env[p[0]] = p[1]
		}
	}
	p, e := game.Lookup(s.Env["SOURCE_GAME"])
	if e != nil {
		return s, e
	}
	s.Identity, e = p.Identity(s.Env["STEAM_BRANCH"])
	if e != nil {
		return s, e
	}
	mode := s.Env["SOURCE_MODE"]
	if mode != "" && mode != "auto" && mode != "managed" && mode != "standalone" {
		return s, errors.New("invalid SOURCE_MODE")
	}
	for _, mount := range c.Mounts {
		if mount.Destination == "/home/container" {
			s.Volume = mount.Source
		}
	}
	if !filepath.IsAbs(s.Volume) || s.Volume == "/" {
		return s, errors.New("server volume not found")
	}
	root, err := os.OpenRoot(s.Volume)
	if err == nil {
		defer root.Close()
		var installed game.Identity
		err = fsx.ReadJSON(root, ".source/identity.json", &installed)
		if err == nil && installed != s.Identity {
			return s, errors.New("installed profile differs from configured game")
		}
		if err != nil && !os.IsNotExist(err) {
			return s, err
		}
	} else if !os.IsNotExist(err) {
		return s, err
	}
	return s, nil
}

type Docker struct {
	HTTP    *http.Client
	Base    string
	mu      sync.Mutex
	version string
}

func NewDocker() *Docker {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	return &Docker{HTTP: &http.Client{Transport: tr, Timeout: 15 * time.Second}, Base: "http://docker"}
}
func (d *Docker) get(ctx context.Context, path string, out any) error {
	r, e := http.NewRequestWithContext(ctx, "GET", d.Base+path, nil)
	if e != nil {
		return e
	}
	resp, e := d.HTTP.Do(r)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Docker HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func (d *Docker) prefix(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.version == "" {
		var v struct {
			APIVersion string `json:"ApiVersion"`
		}
		if e := d.get(ctx, "/version", &v); e != nil {
			return "", e
		}
		if !regexp.MustCompile(`^1\.[0-9]+$`).MatchString(v.APIVersion) {
			return "", errors.New("invalid Docker API version")
		}
		d.version = "/v" + v.APIVersion
	}
	return d.version, nil
}
func (d *Docker) Inspect(ctx context.Context, id string, images []string) (Server, error) {
	if !validID.MatchString(id) {
		return Server{}, errors.New("invalid container ID")
	}
	prefix, e := d.prefix(ctx)
	if e != nil {
		return Server{}, e
	}
	var c dockerInspect
	if e = d.get(ctx, prefix+"/containers/"+url.PathEscape(id)+"/json", &c); e != nil {
		return Server{}, e
	}
	return serverFrom(c, images)
}
func (d *Docker) Servers(ctx context.Context, images []string) ([]Server, error) {
	prefix, e := d.prefix(ctx)
	if e != nil {
		return nil, e
	}
	var list []struct {
		ID    string `json:"Id"`
		Image string
	}
	if e = d.get(ctx, prefix+"/containers/json?all=1", &list); e != nil {
		return nil, e
	}
	var out []Server
	for _, v := range list {
		allowed := false
		for _, image := range images {
			if imageRepo(v.Image) == image {
				allowed = true
			}
		}
		if !allowed {
			continue
		}
		s, e := d.Inspect(ctx, v.ID, images)
		if e == nil {
			out = append(out, s)
		}
	}
	return out, nil
}
func Targets(servers []Server, id game.Identity) []Server {
	var out []Server
	for _, s := range servers {
		if s.Running && s.Identity == id && s.Env["SOURCE_MODE"] != "standalone" {
			out = append(out, s)
		}
	}
	return out
}
