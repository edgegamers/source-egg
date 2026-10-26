package egg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/passwords"
)

func cachedPasswordFixture(t *testing.T, dir string) (passwords.Assignment, string) {
	t.Helper()
	assignment := passwords.Assignment{Password: "amber-lantern-copper", Revision: "0123456789abcdef0123456789abcdef", GeneratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NextAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	if err := os.MkdirAll(filepath.Join(dir, ".source"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(assignment)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".source/password.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return assignment, path
}

func TestDisabledNodeReusesCachedPasswordOnNextBootWithoutRewriting(t *testing.T) {
	cfg := Config{Dir: t.TempDir(), Game: "css", Mode: "standalone", Port: 27015, GeneratePassword: true}
	old, path := cachedPasswordFixture(t, cfg.Dir)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"password_disabled":true}`) }))
	defer server.Close()
	egg := Egg{Config: cfg, Installer: &installRecorder{}, Node: server.Client(), NodeEndpoint: server.URL, Progress: io.Discard}
	prepared, err := egg.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Password != old.Password || prepared.Assignment == nil || *prepared.Assignment != old {
		t.Fatal("disabled node did not retain cached assignment")
	}
	found := false
	for i, arg := range prepared.Args {
		if arg == "+sv_password" && i+1 < len(prepared.Args) && prepared.Args[i+1] == old.Password {
			found = true
		}
	}
	if !found {
		t.Fatal("cached password did not reach server launch arguments")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || string(original) != string(current) {
		t.Fatal("disabled password cache was rewritten")
	}
}

func TestCachedPasswordRequiresExplicitDisabledNode(t *testing.T) {
	for _, kind := range []string{"nil-node", "unreachable-node", "legacy-no-assignment", "disabled-with-assignment"} {
		t.Run(kind, func(t *testing.T) {
			cfg := Config{Dir: t.TempDir(), Game: "css", Mode: "standalone", Port: 27015, GeneratePassword: true}
			_, path := cachedPasswordFixture(t, cfg.Dir)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			egg := Egg{Config: cfg, Installer: &installRecorder{}, Progress: io.Discard}
			if kind != "nil-node" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if kind == "disabled-with-assignment" {
						io.WriteString(w, `{"password_disabled":true,"password":{"password":"new-node-password"}}`)
					} else {
						io.WriteString(w, `{}`)
					}
				}))
				defer server.Close()
				egg.Node, egg.NodeEndpoint = server.Client(), server.URL
				if kind == "unreachable-node" {
					server.Close()
				}
			}
			if _, err = egg.Prepare(context.Background()); err == nil {
				t.Fatal("cached password silently accepted without an unambiguous disabled response")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("failed boot changed cache", err)
			}
		})
	}
}

func TestDisabledNodeRejectsMissingMalformedOrSymlinkPasswordCache(t *testing.T) {
	for _, kind := range []string{"missing", "json", "null", "empty", "no-revision", "no-generated-time", "unsafe-password", "invalid-dates", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			cfg := Config{Dir: t.TempDir(), Game: "css", Mode: "standalone", Port: 27015, GeneratePassword: true}
			old, path := cachedPasswordFixture(t, cfg.Dir)
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "json":
				os.WriteFile(path, []byte("not-json"), 0600)
			case "null":
				os.WriteFile(path, []byte("null"), 0600)
			case "empty":
				old.Password = ""
			case "no-revision":
				old.Revision = ""
			case "no-generated-time":
				old.GeneratedAt = time.Time{}
			case "unsafe-password":
				old.Password = "secret;quit"
			case "invalid-dates":
				old.NextAt = old.GeneratedAt.Add(-time.Hour)
			case "symlink":
				target := filepath.Join(cfg.Dir, "elsewhere.json")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains("empty,no-revision,no-generated-time,unsafe-password,invalid-dates", kind) {
				raw, err := json.Marshal(old)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]bool{"password_disabled": true})
			}))
			defer server.Close()
			egg := Egg{Config: cfg, Installer: &installRecorder{}, Node: server.Client(), NodeEndpoint: server.URL, Progress: io.Discard}
			if _, err := egg.Prepare(context.Background()); err == nil {
				t.Fatal("disabled node booted without a valid private cached assignment")
			}
			if kind == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("missing password cache was generated", err)
				}
			}
		})
	}
}
