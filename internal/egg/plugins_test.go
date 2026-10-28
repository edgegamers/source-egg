package egg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/edgegamers/source-egg/internal/plugins"
)

func TestMalformedPluginManifestWarnsAndStartsViableGame(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"CONSOLE_LOG_LEVEL": "ERROR"})
	writeOptionFile(t, cfg.Dir, ".source/plugins.json", `{"version":1,"plugins":[`)
	writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\ntouch game-started\n")
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
		t.Fatal(err)
	}
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal("invalid plugin recipe prevented otherwise viable startup", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Dir, "game-started")); err != nil {
		t.Fatal("game was not started", err)
	}
	log, err := os.ReadFile(filepath.Join(cfg.Dir, ".source/logs/console.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "SourceEgg | Plugins |") || !strings.Contains(strings.ToLower(string(log)), "configuration") {
		t.Fatalf("plugin configuration failure was silent: %q", log)
	}
}

// Moving plugin deployment after server.Run would let the game load the old or
// absent code even though the owner approved the staged package before restart.
func TestApprovedPluginIsInstalledBeforeStandaloneAndManagedGameLaunch(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(fmt.Sprintf("managed=%t", managed), func(t *testing.T) {
			cfg := optionsConfig(t, map[string]string{"CONSOLE_LOG_LEVEL": "ERROR"})
			var releases *httptest.Server
			releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/kit.smx" {
					io.WriteString(w, "approved-v1")
					return
				}
				if r.URL.Path != "/repos/example/kit/releases" {
					t.Errorf("unexpected release request: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				fmt.Fprintf(w, `[{"id":1,"tag_name":"v1","published_at":"2026-10-01T00:00:00Z","assets":[{"name":"kit.smx","browser_download_url":%q}]}]`, releases.URL+"/kit.smx")
			}))
			defer releases.Close()
			manifest := plugins.Manifest{Version: 1, Plugins: []plugins.Plugin{{ID: "kit", Source: plugins.Source{Provider: "github", Repository: "example/kit", URL: releases.URL}, Asset: "kit.smx", Format: "raw", Mappings: []plugins.Mapping{{From: "kit.smx", To: "addons/sourcemod/plugins/kit.smx"}}}}}
			body, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			writeOptionFile(t, cfg.Dir, ".source/plugins.json", string(body))
			var notices []string
			manager, err := plugins.New(cfg.Dir, "cstrike", nil, func(line string) { notices = append(notices, line) })
			if err != nil {
				t.Fatal(err)
			}
			if err = manager.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			manager.Command(context.Background(), "egg plugins preview kit")
			match := regexp.MustCompile(`package=([a-f0-9]{64})`).FindStringSubmatch(strings.Join(notices, "\n"))
			if len(match) != 2 {
				t.Fatalf("preview did not stage a package: %q", notices)
			}
			manager.Command(context.Background(), "egg plugins approve kit "+match[1])
			if _, err = os.Stat(filepath.Join(cfg.Dir, "cstrike/addons/sourcemod/plugins/kit.smx")); !os.IsNotExist(err) {
				t.Fatal("approval changed game files before restart")
			}
			writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\ncat cstrike/addons/sourcemod/plugins/kit.smx > launched-plugin\n")
			if err = os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
				t.Fatal(err)
			}
			e := Egg{Config: cfg, Installer: &installRecorder{}}
			if managed {
				node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, `{"managed":true,"delivered":true,"cleanup_managed":true,"generation":{"identity":{"game":"css","app_id":"232330","branch":"public","runtime":"linux-auto"},"key":"generations/css/public/linux-auto/fixture"}}`)
				}))
				defer node.Close()
				e.Node, e.NodeEndpoint, e.Config.Mode = node.Client(), node.URL, "managed"
			}
			if err = e.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			loaded, err := os.ReadFile(filepath.Join(cfg.Dir, "launched-plugin"))
			if err != nil || string(loaded) != "approved-v1" {
				t.Fatalf("game did not launch with approved plugin: %q, %v (notices %q)", loaded, err, notices)
			}
		})
	}
}

func TestPluginRecoveryFailureStopsBeforeGamePreparation(t *testing.T) {
	cfg := optionsConfig(t, nil)
	writeOptionFile(t, cfg.Dir, ".source/plugins/journal.json", `{"interrupted":`)
	writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\ntouch game-started\n")
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
		t.Fatal(err)
	}
	installer := &installRecorder{}
	e := Egg{Config: cfg, Installer: installer}
	if err := e.Run(context.Background()); err == nil {
		t.Fatal("started with an unrecoverable plugin transaction")
	}
	if installer.calls != 0 {
		t.Fatal("game preparation ran before interrupted plugin recovery")
	}
	if _, err := os.Stat(filepath.Join(cfg.Dir, "game-started")); !os.IsNotExist(err) {
		t.Fatal("game started before interrupted plugin recovery")
	}
}
