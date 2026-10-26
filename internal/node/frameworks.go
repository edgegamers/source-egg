package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/fsx"
)

type FrameworkPolicy struct {
	Fleet   map[string]string            `json:"fleet"`
	Servers map[string]map[string]string `json:"servers"`
}

func (d *Daemon) loadFrameworkPolicy() error {
	d.frameworkPolicy = FrameworkPolicy{Fleet: map[string]string{}, Servers: map[string]map[string]string{}}
	root, e := os.OpenRoot(d.Config.StateDir)
	if e != nil {
		return e
	}
	defer root.Close()
	e = fsx.ReadJSON(root, "framework-pins.json", &d.frameworkPolicy)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if d.frameworkPolicy.Fleet == nil || d.frameworkPolicy.Servers == nil {
		return errors.New("invalid framework pin state")
	}
	return nil
}
func (d *Daemon) frameworkPin(s Server, name string) string {
	if pin := s.Env[strings.ToUpper(name)+"_VERSION"]; pin != "" {
		return pin
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.Config.FrameworkPinsEnabled {
		return ""
	}
	if pin, ok := d.frameworkPolicy.Servers[s.UUID][name]; ok {
		return pin
	}
	return d.frameworkPolicy.Fleet[name]
}
func (d *Daemon) setFrameworkPin(ctx context.Context, name, pin, server string, verify func(context.Context, string, string) error) error {
	if name != "metamod" && name != "sourcemod" && name != "all" {
		return errors.New("framework must be metamod or sourcemod")
	}
	if server != "" && !validID.MatchString(server) {
		return errors.New("invalid server")
	}
	if pin == "latest" {
		pin = ""
	}
	if len(pin) > 128 || strings.ContainsAny(pin, "\n\r\x00/") {
		return errors.New("invalid framework version")
	}
	if pin != "" {
		if name == "all" {
			return errors.New("all is only valid when removing pins")
		}
		if verify == nil {
			return errors.New("version verification is required")
		}
		if e := verify(ctx, name, pin); e != nil {
			return e
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	next := FrameworkPolicy{Fleet: map[string]string{}, Servers: map[string]map[string]string{}}
	for k, v := range d.frameworkPolicy.Fleet {
		next.Fleet[k] = v
	}
	for id, pins := range d.frameworkPolicy.Servers {
		next.Servers[id] = map[string]string{}
		for k, v := range pins {
			next.Servers[id][k] = v
		}
	}
	if name == "all" {
		if server == "" {
			next = FrameworkPolicy{Fleet: map[string]string{}, Servers: map[string]map[string]string{}}
		} else {
			delete(next.Servers, server)
		}
	} else if server == "" {
		if pin == "" {
			delete(next.Fleet, name)
		} else {
			next.Fleet[name] = pin
		}
	} else {
		if next.Servers[server] == nil {
			next.Servers[server] = map[string]string{}
		}
		next.Servers[server][name] = pin
	}
	root, e := os.OpenRoot(d.Config.StateDir)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = fsx.JSON(root, "framework-pins.json", next, 0600); e != nil {
		return e
	}
	d.frameworkPolicy = next
	return nil
}
func (d *Daemon) selectServer(selector string) (Server, error) {
	if !validID.MatchString(selector) {
		return Server{}, errors.New("invalid server identifier")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.servers[selector]; ok {
		return s, nil
	}
	var match Server
	count := 0
	for id, s := range d.servers {
		if strings.HasPrefix(id, selector) {
			match = s
			count++
		}
	}
	if count != 1 {
		return Server{}, fmt.Errorf("server prefix must match exactly one server (matched %d)", count)
	}
	return match, nil
}

type FrameworkView struct {
	Server, Game, Name string
	Installed          map[string]string
	Selected           map[string]string
}

func (d *Daemon) frameworkInventory() any {
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	for _, s := range d.servers {
		servers = append(servers, s)
	}
	policy := d.frameworkPolicy
	d.mu.Unlock()
	out := []FrameworkView{}
	for _, s := range servers {
		v := FrameworkView{Server: s.UUID, Game: s.Identity.Game, Name: s.Env["SERVER_HOSTNAME"], Installed: map[string]string{}, Selected: map[string]string{}}
		root, e := os.OpenRoot(s.Volume)
		for _, name := range []string{"metamod", "sourcemod"} {
			v.Selected[name] = d.frameworkPin(s, name)
			if e == nil {
				var receipt addons.Receipt
				if fsx.ReadJSON(root, ".source/framework-"+name+".json", &receipt) == nil {
					v.Installed[name] = receipt.Version
				}
			}
		}
		if e == nil {
			root.Close()
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return struct {
		Policy  FrameworkPolicy `json:"policy"`
		Servers []FrameworkView `json:"servers"`
	}{policy, out}
}
