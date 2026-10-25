package egg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/content"
	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/passwords"
)

type Egg struct {
	Config       Config
	Installer    content.Installer
	Cache        *addons.Cache
	Node         *http.Client
	NodeEndpoint string
	Progress     io.Writer
}

func (e *Egg) progress(message string) {
	out := e.Progress
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out, "SourceEgg | "+message)
}

type Prepared struct {
	Managed    bool
	Profile    game.Profile
	Identity   game.Identity
	Generation string
	Password   string
	Assignment *passwords.Assignment
	Args       []string
}

func (e *Egg) call(ctx context.Context, r node.Request) (node.Response, error) {
	if e.Node == nil {
		return node.Response{}, errors.New("node is unavailable")
	}
	url := e.NodeEndpoint
	if url == "" {
		url = "http://sourcenode/"
	}
	return node.CallURL(ctx, e.Node, url, r)
}
func (e *Egg) Prepare(ctx context.Context) (Prepared, error) {
	c := e.Config
	result := Prepared{}
	p, err := game.Lookup(c.Game)
	if err != nil {
		return result, err
	}
	id, err := p.Identity(c.Branch)
	if err != nil {
		return result, err
	}
	result.Profile = p
	result.Identity = id
	e.progress("Preparing " + p.Name)
	if err = os.MkdirAll(c.Dir, 0755); err != nil {
		return result, err
	}
	if err = game.CheckIdentity(c.Dir, id); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(c.Dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = root.Chmod(".source", 0700); err != nil {
		return result, err
	}
	_, managedErr := root.Lstat(".source/managed.json")
	wasManaged := managedErr == nil
	if managedErr != nil && !os.IsNotExist(managedErr) {
		return result, managedErr
	}
	e.progress("Checking for node-managed files...")
	if e.Node == nil && c.NodeWaitSeconds > 0 {
		deadline := time.NewTimer(time.Duration(c.NodeWaitSeconds) * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		waiting := true
		for waiting {
			if _, err = os.Stat(node.SocketDir + "/node.sock"); err == nil {
				e.Node = node.SocketClient(node.SocketDir + "/node.sock")
				break
			}
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-deadline.C:
				waiting = false
			case <-ticker.C:
			}
		}
	}
	var response node.Response
	if e.Node != nil {
		e.progress("Connected to node; preparing shared files, framework cache and password policy...")
		response, err = e.call(ctx, node.Request{Action: "prepare", Identity: id})
		if err != nil {
			return result, err
		}
	}
	if (c.Mode == "managed" || wasManaged) && !response.Managed {
		return result, errors.New("managed files require a ready node and read-only mount; refusing local SteamCMD fallback")
	}
	result.Managed = response.Managed
	if response.Managed {
		e.progress("Using node-managed base game files; local SteamCMD skipped")
		g := response.Generation
		if g.Identity != id {
			return result, errors.New("node returned wrong game generation")
		}
		g.Path = filepath.Join(node.SharedMount, g.Key)
		if err = content.Deploy(g, c.Dir, node.SharedMount); err != nil {
			return result, err
		}
		result.Generation = g.Key
	} else {
		if e.Installer == nil {
			if err = SeedSteamCMD("/opt/steamcmd", c.Dir); err != nil {
				return result, err
			}
			e.Installer = &content.Steam{Executable: c.SteamCMD, Username: c.SteamUsername, Password: c.SteamPassword, Validate: c.Validate}
		}
		_, installedErr := content.InstalledBuild(c.Dir, id)
		if c.UpdateGame || installedErr != nil {
			e.progress("Installing/updating " + p.Name + " through SteamCMD; first download can take a while...")
			if err = e.Installer.Install(ctx, id, c.Dir); err != nil {
				return result, err
			}
		}
	}
	if c.GeneratePassword {
		if response.Password == nil || response.Password.Password == "" {
			return result, errors.New("generated password requested but no node assignment available")
		}
		result.Assignment = response.Password
		result.Password = response.Password.Password
		if err = fsx.JSON(root, ".source/password.json", response.Password, 0600); err != nil {
			return result, err
		}
	} else {
		result.Password = c.Password
	}
	if p.Key == "csgo" {
		if err = configureCSGO(root, p.ClientAppID); err != nil {
			return result, err
		}
	}
	if err = e.frameworks(ctx, p, response); err != nil {
		return result, err
	}
	for _, arch := range []string{"32", "64"} {
		source := filepath.Join("/opt/steamcmd", "linux"+arch, "steamclient.so")
		if !response.Managed {
			source = filepath.Join(filepath.Dir(c.SteamCMD), "linux"+arch, "steamclient.so")
		}
		if _, err = os.Stat(source); err == nil {
			if err = fsx.Copy(root, ".steam/sdk"+arch+"/steamclient.so", source, 0755); err != nil {
				return result, err
			}
		}
	}
	result.Args, err = p.Command(game.Launch{Port: c.Port, MaxPlayers: c.MaxPlayers, Map: c.Map, Hostname: c.Hostname, Password: result.Password, RCON: c.RCON, GSLT: c.GSLT})
	if err != nil {
		return result, err
	}
	if response.Managed {
		if _, err = e.call(ctx, node.Request{Action: "applied", Identity: id, Generation: result.Generation}); err != nil {
			return result, err
		}
	}
	return result, nil
}
func (e *Egg) frameworks(ctx context.Context, p game.Profile, response node.Response) error {
	installed := map[string]bool{}
	for _, f := range response.Frameworks {
		e.progress("Applying cached " + f.Package.Name + " " + f.Package.Version)
		if !filepath.IsLocal(f.Archive) {
			return errors.New("invalid framework cache path")
		}
		if f.Package.Name != "metamod" && f.Package.Name != "sourcemod" {
			return errors.New("unknown cached framework")
		}
		if err := addons.Deploy(filepath.Join(node.CacheMount, f.Archive), e.Config.Dir, p.Dir, f.Package.Name, f.Package.Version); err != nil {
			return err
		}
		installed[f.Package.Name] = true
	}
	for _, name := range []string{"metamod", "sourcemod"} {
		enabled := e.Config.InstallMetamod || e.Config.InstallSourceMod
		pin := e.Config.MetamodVersion
		if name == "sourcemod" {
			enabled = e.Config.InstallSourceMod
			pin = e.Config.SourceModVersion
		}
		if !enabled || installed[name] {
			continue
		}
		e.progress("Checking " + name + " release and preparing private installation...")
		if e.Cache == nil {
			e.Cache = addons.NewCache(filepath.Join(e.Config.Dir, ".source/cache"), nil)
		}
		pkg, err := e.Cache.Resolve(ctx, name, pin)
		if err != nil {
			return fmt.Errorf("%s resolution: %w", name, err)
		}
		archive, err := e.Cache.Fetch(ctx, pkg)
		if err != nil {
			return err
		}
		if err = addons.Deploy(archive, e.Config.Dir, p.Dir, name, pkg.Version); err != nil {
			return err
		}
	}
	return nil
}
func configureCSGO(root *os.Root, clientApp string) error {
	data, err := root.ReadFile("csgo/steam.inf")
	if err != nil {
		return fmt.Errorf("CS:GO steam.inf unavailable: %w", err)
	}
	pattern := regexp.MustCompile(`(?mi)^appID[ \t]*=[ \t]*[0-9]+[ \t]*$`)
	if !pattern.Match(data) {
		return errors.New("CS:GO steam.inf has no appID entry")
	}
	data = pattern.ReplaceAll(data, []byte("appID="+clientApp))
	if err = fsx.Atomic(root, "csgo/steam.inf", data, 0644); err != nil {
		return err
	}
	return fsx.Atomic(root, "steam_appid.txt", []byte(clientApp+"\n"), 0644)
}

var credentialLine = regexp.MustCompile(`(?i)("?(?:rcon_password|sv_password|sv_setsteamaccount)"?\s*(?:=\s*)?)(?:"[^"]*"|[^\r\n]+)`)

func Mask(line string, secrets []string) string {
	for _, s := range secrets {
		if s != "" {
			line = strings.ReplaceAll(line, s, "[redacted]")
		}
	}
	return credentialLine.ReplaceAllString(line, "${1}[redacted]")
}
