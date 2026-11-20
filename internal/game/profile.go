package game

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/edgegamers/source-egg/internal/fsx"
)

type Profile struct{ Key, Name, AppID, ClientAppID, Dir, Executable, Runtime, DefaultMap string }

var profiles = []Profile{
	{"csgo", "CS:GO", "740", "4465480", "csgo", "./srcds_run", "linux-auto", "de_dust2"},
	{"css", "CS:S", "232330", "240", "cstrike", "./srcds_run", "linux-auto", "de_dust2"},
	{"dods", "DOD:S", "232290", "300", "dod", "./srcds_run", "linux-auto", "dod_avalanche"},
	{"gmod", "GMod", "4020", "4000", "garrysmod", "./srcds_run", "linux32", "gm_construct"},
	{"l4d2", "L4D2", "222860", "550", "left4dead2", "./srcds_run", "linux32", "c1m1_hotel"},
	{"tf2", "TF2", "232250", "440", "tf", "./srcds_run_64", "linux64", "cp_dustbowl"},
}

func All() []Profile { return append([]Profile(nil), profiles...) }
func Lookup(key string) (Profile, error) {
	if key == "garrysmod" || key == "Garry's Mod" {
		key = "gmod"
	}
	for _, p := range profiles {
		if key == p.Key || key == p.Name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("unsupported Source game %q", key)
}

// ForBranch selects launch details without changing the registered defaults.
func (p Profile) ForBranch(branch string) Profile {
	if p.Key == "gmod" {
		p.Executable, p.Runtime = "./srcds_run", "linux32"
		if branch == "x86-64" {
			p.Executable, p.Runtime = "./srcds_run_x64", "linux64"
		}
	}
	return p
}

// SupportsFrameworks excludes GMod, which uses its own Lua addon system.
func (p Profile) SupportsFrameworks() bool { return p.Key != "gmod" }

type Identity struct {
	Game    string `json:"game"`
	AppID   string `json:"app_id"`
	Branch  string `json:"branch"`
	Runtime string `json:"runtime"`
}

var safePart = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (p Profile) Identity(branch string) (Identity, error) {
	if branch == "" {
		branch = "public"
	}
	if !safePart.MatchString(branch) {
		return Identity{}, errors.New("invalid Steam branch")
	}
	p = p.ForBranch(branch)
	return Identity{p.Key, p.AppID, branch, p.Runtime}, nil
}
func (i Identity) Key() string { return i.Game + "/" + i.Branch + "/" + i.Runtime }
func (i Identity) Validate() error {
	p, e := Lookup(i.Game)
	if e != nil {
		return e
	}
	v, e := p.Identity(i.Branch)
	if e != nil {
		return e
	}
	if i != v {
		return errors.New("identity does not match registered game profile")
	}
	return nil
}

// CheckIdentity permanently binds this installation to its game/branch/runtime.
func CheckIdentity(dir string, want Identity) error {
	if e := want.Validate(); e != nil {
		return e
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer root.Close()
	var have Identity
	e = fsx.ReadJSON(root, ".source/identity.json", &have)
	if e == nil {
		if have != want {
			return errors.New("installed game/branch differs; use a new volume or explicit reinstall")
		}
		return nil
	}
	if !os.IsNotExist(e) {
		return e
	}
	return fsx.JSON(root, ".source/identity.json", want, 0600)
}
