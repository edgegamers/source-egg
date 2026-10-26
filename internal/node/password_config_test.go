package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/passwords"
)

func TestPasswordDirectoryFollowsPrivateStateAndAllowsOverride(t *testing.T) {
	config := DefaultConfig()
	config.StateDir = t.TempDir()
	if got, want := config.passwordDirectory(), filepath.Join(config.StateDir, "passwords"); got != want {
		t.Fatalf("default path %s != %s", got, want)
	}
	config.PasswordDir = filepath.Join(t.TempDir(), "custom-passwords")
	if got := config.passwordDirectory(); got != config.PasswordDir {
		t.Fatalf("override ignored: %s", got)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative", "/", filepath.Join(config.SharedDir, "passwords"), filepath.Join(config.CacheDir, "passwords")} {
		config.PasswordDir = path
		if err := config.Validate(); err == nil {
			t.Fatalf("accepted unsafe private password path %s", path)
		}
	}
}

func TestDisabledPasswordsPreserveCachedAssignments(t *testing.T) {
	config := DefaultConfig()
	config.StateDir = t.TempDir()
	config.PasswordDir = filepath.Join(t.TempDir(), "passwords")
	store, err := passwords.Open(config.PasswordDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Get("game", "a", "css", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(config.PasswordDir, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	config.PasswordEnabled = false
	daemon, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Observation.Close()
	server := Server{UUID: "a", Identity: game.Identity{Game: "css"}, Env: map[string]string{"GENERATE_PASSWORD": "1"}}
	if _, err := daemon.password(context.Background(), server); err == nil {
		t.Fatal("disabled password module generated or delivered a password")
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("disabled module rewrote persisted assignment: %v", err)
	}
	reopened, err := passwords.Open(config.PasswordDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := reopened.Get("game", "a", "css", old.GeneratedAt)
	if err != nil || retained != old {
		t.Fatalf("disabled module lost cached password: %v", err)
	}
}

func TestDisabledPasswordsDoNotNeedCustomDictionary(t *testing.T) {
	config := DefaultConfig()
	config.StateDir = t.TempDir()
	config.PasswordDir = filepath.Join(t.TempDir(), "not-created")
	config.PasswordEnabled = false
	config.PasswordFormat = "eff"
	config.PasswordWordlist = filepath.Join(t.TempDir(), "missing-words.txt")
	daemon, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Observation.Close()
	if _, err := os.Stat(config.PasswordDir); !os.IsNotExist(err) {
		t.Fatalf("disabled module created password state: %v", err)
	}
}

func TestModuleStatusReportsPasswordMasterSwitch(t *testing.T) {
	config := DefaultConfig()
	config.StateDir = t.TempDir()
	daemon, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Observation.Close()
	for _, want := range []bool{false, true} {
		daemon.Config.PasswordEnabled = want
		raw, err := json.Marshal(daemon.moduleStatus())
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			PasswordEnabled *bool `json:"password_enabled"`
		}
		if err = json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if response.PasswordEnabled == nil || *response.PasswordEnabled != want {
			t.Fatalf("module summary omitted password switch: %s", raw)
		}
	}
}

func TestPasswordStorageRejectsAliasesBeforeRuntimeWrites(t *testing.T) {
	for _, publicName := range []string{"shared", "cache"} {
		for _, kind := range []string{"leaf-alias", "parent-alias-existing", "parent-alias-missing", "public-alias"} {
			t.Run(publicName+"/"+kind, func(t *testing.T) {
				base := t.TempDir()
				config := DefaultConfig()
				config.StateDir = filepath.Join(base, "private-state")
				config.SharedDir = filepath.Join(base, "shared")
				config.CacheDir = filepath.Join(base, "cache")
				public := config.SharedDir
				if publicName == "cache" {
					public = config.CacheDir
				}
				if err := os.MkdirAll(public, 0755); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(public, "passwords")
				alias := filepath.Join(base, "alias")
				switch kind {
				case "leaf-alias":
					if err := os.Mkdir(target, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, alias); err != nil {
						t.Fatal(err)
					}
					config.PasswordDir = alias
				case "parent-alias-existing", "parent-alias-missing":
					if kind == "parent-alias-existing" {
						if err := os.Mkdir(target, 0755); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(public, alias); err != nil {
						t.Fatal(err)
					}
					config.PasswordDir = filepath.Join(alias, "passwords")
				case "public-alias":
					if err := os.Symlink(public, alias); err != nil {
						t.Fatal(err)
					}
					if publicName == "shared" {
						config.SharedDir = alias
					} else {
						config.CacheDir = alias
					}
					config.PasswordDir = target
				}
				raw, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				configPath := filepath.Join(base, "config.json")
				if err = os.WriteFile(configPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err = LoadConfig(configPath); err == nil {
					t.Error("LoadConfig accepted password storage exposed through public alias")
				}
				daemon, err := New(config)
				if err == nil {
					daemon.Observation.Close()
					t.Error("New accepted password storage exposed through public alias")
				}
				if _, err = os.Stat(config.StateDir); !os.IsNotExist(err) {
					t.Error("rejected config created node state", err)
				}
				if _, err = os.Stat(filepath.Join(target, "state.json")); !os.IsNotExist(err) {
					t.Error("password state appeared in public mount", err)
				}
				info, err := os.Stat(target)
				if kind == "leaf-alias" || kind == "parent-alias-existing" {
					if err != nil || info.Mode().Perm() != 0755 {
						t.Error("rejected config changed public directory permissions", err)
					}
				} else if !os.IsNotExist(err) {
					t.Error("rejected config created public password directory", err)
				}
			})
		}
	}
}

func TestRuntimeAcceptsDedicatedPrivatePasswordDirectory(t *testing.T) {
	base := t.TempDir()
	config := DefaultConfig()
	config.StateDir = filepath.Join(base, "state")
	config.SharedDir = filepath.Join(base, "shared")
	config.CacheDir = filepath.Join(base, "cache")
	config.PasswordDir = filepath.Join(base, "private", "new-passwords")
	daemon, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Observation.Close()
	if _, err = daemon.Passwords.Get("game", "a", "css", time.Now()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(config.PasswordDir, "state.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private assignment not persisted safely: %v", err)
	}
}

func TestDisabledPasswordProtocolIsExplicitWithoutGenerating(t *testing.T) {
	config := DefaultConfig()
	config.StateDir = t.TempDir()
	config.PasswordDir = filepath.Join(t.TempDir(), "not-created")
	config.PasswordEnabled = false
	config.Shared = false
	config.FrameworkCache = false
	daemon, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Observation.Close()
	server := Server{UUID: "a", Identity: game.Identity{Game: "css"}, Env: map[string]string{"GENERATE_PASSWORD": "1"}}
	for _, action := range []string{"prepare", "password"} {
		response, err := daemon.handle(context.Background(), server, Request{Action: action})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		raw, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Disabled bool                  `json:"password_disabled"`
			Password *passwords.Assignment `json:"password"`
		}
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if !decoded.Disabled || decoded.Password != nil {
			t.Fatalf("%s did not explicitly disable generation: %s", action, raw)
		}
	}
	if _, err = os.Stat(config.PasswordDir); !os.IsNotExist(err) {
		t.Fatal("disabled protocol created password state", err)
	}
}
