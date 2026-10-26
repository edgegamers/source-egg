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
)

func optionsConfig(t *testing.T, extra map[string]string) Config {
	t.Helper()
	env := map[string]string{"SOURCE_GAME": "css", "SERVER_PORT": "27015", "INSTALL_METAMOD": "0", "INSTALL_SOURCEMOD": "0", "NODE_WAIT_SECONDS": "0"}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	cfg.Dir = t.TempDir()
	return cfg
}
func writeOptionFile(t *testing.T, dir, name, body string) {
	t.Helper()
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPanelReadinessSurvivesEveryConsoleFilterAndThreshold(t *testing.T) {
	raw, err := os.ReadFile("../../pterodactyl/source-egg.json")
	if err != nil {
		t.Fatal(err)
	}
	var egg struct {
		Config struct {
			Startup string `json:"startup"`
		} `json:"config"`
	}
	if err = json.Unmarshal(raw, &egg); err != nil {
		t.Fatal(err)
	}
	var startup struct {
		Done string `json:"done"`
	}
	if err = json.Unmarshal([]byte(egg.Config.Startup), &startup); err != nil {
		t.Fatal(err)
	}
	if startup.Done != panelReadyLine {
		t.Fatal("panel readiness contract differs from runtime", startup.Done)
	}
	for _, level := range []int{0, 1, 2, 3} {
		for _, preview := range []bool{false, true} {
			s := consoleSettings{enabled: true, level: level, filter: consoleFilter{Patterns: []string{"SourceEgg", "@" + startup.Done}, PreviewMode: preview}}
			got, show := s.display(startup.Done)
			if !show || got != startup.Done {
				t.Errorf("level %d preview %t hid or changed panel readiness: %q, %t", level, preview, got, show)
			}
		}
	}
}

func TestExtraArgumentsAreQuotedWithoutShellAndCannotOverrideManagedFlags(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"CUSTOM_PARAMS": `-tickrate 100 +sv_tags "community server" +motdfile 'my motd.txt'`})
	e := Egg{Config: cfg, Installer: &installRecorder{}, Progress: io.Discard}
	p, err := e.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Args, "\n")
	if !strings.Contains(got, "-tickrate\n100\n+sv_tags\ncommunity server\n+motdfile\nmy motd.txt") {
		t.Fatalf("missing quoted argv: %q", p.Args)
	}
	for _, input := range []string{`-game tf`, `-port=1`, `+sv_password bypass`, `-binary /bin/sh`, `-debug`, `+hostname "unterminated`, "x\nquit", `+sv_tags "x;quit"`, "$(touch /tmp/no)", "`id`", strings.Repeat("x", 8193)} {
		env := map[string]string{"SOURCE_GAME": "css", "SERVER_PORT": "27015", "CUSTOM_PARAMS": input}
		if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
			t.Errorf("accepted unsafe params %q", input)
		}
	}
}

func TestStandaloneCleanupIsOptInRootedAndYieldsToNode(t *testing.T) {
	for _, tc := range []struct {
		name          string
		enabled, node bool
		wantGone      bool
	}{{"disabled", false, false, false}, {"standalone", true, false, true}, {"node", true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			enabled := "0"
			if tc.enabled {
				enabled = "1"
			}
			cfg := optionsConfig(t, map[string]string{"CLEANUP_ENABLED": enabled})
			writeOptionFile(t, cfg.Dir, ".source/configs/cleanup.json", `{"rules":[{"name":"old demos","directories":["cstrike"],"patterns":["*.dem"],"hours":0}]}`)
			writeOptionFile(t, cfg.Dir, "cstrike/old.dem", "old")
			writeOptionFile(t, cfg.Dir, "cstrike/server.cfg", "private")
			outside := t.TempDir()
			writeOptionFile(t, outside, "outside.dem", "keep")
			if err := os.Symlink(outside, filepath.Join(cfg.Dir, "cstrike/link")); err != nil {
				t.Fatal(err)
			}
			e := Egg{Config: cfg, Installer: &installRecorder{}, Progress: io.Discard}
			if tc.node {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"cleanup_managed":true}`) }))
				defer s.Close()
				e.Node = s.Client()
				e.NodeEndpoint = s.URL
			}
			if _, err := e.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(cfg.Dir, "cstrike/old.dem"))
			if os.IsNotExist(err) != tc.wantGone {
				t.Fatalf("cleanup deletion = %v, want %v", os.IsNotExist(err), tc.wantGone)
			}
			for _, path := range []string{filepath.Join(cfg.Dir, "cstrike/server.cfg"), filepath.Join(outside, "outside.dem")} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("lost protected file", err)
				}
			}
		})
	}
}

func TestCleanupRejectsEscapesAndOversizedConfigWithoutDeletingFiles(t *testing.T) {
	for _, body := range []string{`{"rules":[{"name":"escape","directories":["../"],"patterns":["*"],"hours":0}]}`, `{"rules":[{"name":"age","directories":["."],"patterns":["*"],"hours":876001}]}`, strings.Repeat(" ", 256<<10) + `{}`} {
		cfg := optionsConfig(t, map[string]string{"CLEANUP_ENABLED": "1"})
		writeOptionFile(t, cfg.Dir, ".source/configs/cleanup.json", body)
		writeOptionFile(t, cfg.Dir, "keep.dem", "keep")
		e := Egg{Config: cfg, Installer: &installRecorder{}, Progress: io.Discard}
		if _, err := e.Prepare(context.Background()); err == nil {
			t.Fatal("accepted unsafe cleanup config")
		}
		if _, err := os.Stat(filepath.Join(cfg.Dir, "keep.dem")); err != nil {
			t.Fatal("removed file with invalid config")
		}
	}
}

func TestRunFiltersMaskedConsoleButKeepsCompleteLocalLog(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"ENABLE_FILTER": "1", "CONSOLE_LOG_LEVEL": "WARNING", "SERVER_PASSWORD": "secret-value"})
	writeOptionFile(t, cfg.Dir, ".source/configs/console-filter.json", `{"patterns":["noisy","secret-value","@exact"]}`)
	writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\nprintf '%s\\n' '[INFO] normal' '[WARNING] noisy' '[WARNING] secret-value' '[ERROR] useful'\n")
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "normal") || strings.Contains(got, "noisy") || strings.Contains(got, "secret-value") || !strings.Contains(got, "[WARNING]") || !strings.Contains(got, "useful") {
		t.Fatalf("bad console filtering: %q", got)
	}
	logs, err := os.ReadFile(filepath.Join(cfg.Dir, ".source/logs/console.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logs), "normal") || !strings.Contains(string(logs), "noisy") || strings.Contains(string(logs), "secret-value") {
		t.Fatalf("bad persisted log: %q", logs)
	}
}

func TestDebugPortValidation(t *testing.T) {
	for _, port := range []string{"0", "-1", "65536", "oops", "27015", "12;sh"} {
		env := map[string]string{"SOURCE_GAME": "css", "SERVER_PORT": "27015", "GDB_DEBUG_PORT": port}
		if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
			t.Errorf("accepted debugger port %q", port)
		}
	}
}

func TestSourceModSwitchesAreStrictAndPreserveDefaults(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"SOURCEMOD_PLUGIN_BASECHAT": "0", "SOURCEMOD_PLUGIN_NOMINATIONS": "1", "SOURCEMOD_PLUGIN_MAPCHOOSER": "1"})
	if cfg.SourceModPlugins["basechat"] != "0" || cfg.SourceModPlugins["nominations"] != "1" || cfg.SourceModPlugins["mapchooser"] != "1" {
		t.Fatalf("lost plugin choices: %v", cfg.SourceModPlugins)
	}
	env := map[string]string{"SOURCE_GAME": "css", "SERVER_PORT": "27015", "SOURCEMOD_PLUGIN_BASECHAT": "enable"}
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("accepted invalid stock plugin policy")
	}
}

func TestDebugStartupExecutesGDBServerWithRealBinaryAndLiteralArguments(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"GDB_DEBUG_PORT": "27016", "CUSTOM_PARAMS": `+sv_tags "debug server"`})
	bin := t.TempDir()
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	writeOptionFile(t, bin, "gdbserver", "#!/bin/sh\nprintf '%s\\n' \"$@\" > debugger-args\nprintf '%s\\n' \"$LD_LIBRARY_PATH\" > debugger-libs\n")
	os.Chmod(filepath.Join(bin, "gdbserver"), 0755)
	writeOptionFile(t, cfg.Dir, "srcds_linux64", "#!/bin/sh\nexit 0\n")
	os.Chmod(filepath.Join(cfg.Dir, "srcds_linux64"), 0755)
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(filepath.Join(cfg.Dir, "debugger-args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(args), "--once\n0.0.0.0:27016\n./srcds_linux64\n-game\ncstrike\n") || !strings.Contains(string(args), "+sv_tags\ndebug server\n") {
		t.Fatalf("unexpected debugger argv: %q", args)
	}
	libs, err := os.ReadFile(filepath.Join(cfg.Dir, "debugger-libs"))
	if err != nil || !strings.Contains(string(libs), filepath.Join(cfg.Dir, "bin/linux64")) {
		t.Fatalf("missing runtime library path: %q, %v", libs, err)
	}
}

func TestConsoleFileLevelExactFilterPreviewAndConfigPreservation(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"ENABLE_FILTER": "1"})
	logging := `{"logging":{"console_level":"DEBUG"},"custom":"keep"}`
	writeOptionFile(t, cfg.Dir, ".source/configs/logging.json", logging)
	writeOptionFile(t, cfg.Dir, ".source/configs/console-filter.json", `{"preview_mode":true,"patterns":["@exact","substring"]}`)
	e := Egg{Config: cfg}
	settings, err := e.consoleSettings()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input, want string
		show        bool
	}{{"exact", "SourceEgg | DEBUG | Filter preview: exact", true}, {"exact extra", "exact extra", true}, {"has substring here", "SourceEgg | DEBUG | Filter preview: has substring here", true}, {"", "", true}} {
		got, show := settings.display(tc.input)
		if got != tc.want || show != tc.show {
			t.Errorf("display(%q)=%q,%v", tc.input, got, show)
		}
	}
	e.Config.ConsoleLogLevel = "ERROR"
	settings, err = e.consoleSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, show := settings.display("[INFO] info"); show {
		t.Fatal("file level overrode explicit environment setting")
	}
	if _, show := settings.display("[ERROR] error"); !show {
		t.Fatal("hid error")
	}
	data, _ := os.ReadFile(filepath.Join(cfg.Dir, ".source/configs/logging.json"))
	if string(data) != logging {
		t.Fatal("overwrote custom settings")
	}
	e.Config.EnableFilter = false
	e.Config.ConsoleLogLevel = "DEBUG"
	settings, err = e.consoleSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got, show := settings.display("exact"); !show || got != "exact" {
		t.Fatal("disabled filter changed console output")
	}
}

func TestPanelDefaultsProduceValidatedLaunchSettings(t *testing.T) {
	data, err := os.ReadFile("../../pterodactyl/source-egg.json")
	if err != nil {
		t.Fatal(err)
	}
	var panel struct {
		Startup   string `json:"startup"`
		Variables []struct {
			Env     string `json:"env_variable"`
			Default string `json:"default_value"`
		} `json:"variables"`
	}
	if err = json.Unmarshal(data, &panel); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, v := range panel.Variables {
		env[v.Env] = v.Default
	}
	for _, name := range []string{"CUSTOM_PARAMS", "CLEANUP_ENABLED", "ENABLE_FILTER", "CONSOLE_LOG_LEVEL", "GDB_DEBUG_PORT", "SOURCEMOD_PLUGIN_BASECHAT", "SOURCEMOD_PLUGIN_ROCKTHEVOTE"} {
		if _, ok := env[name]; !ok {
			t.Errorf("missing panel control %s", name)
		}
	}
	env["SERVER_PORT"] = "27015"
	for _, name := range []string{"css", "csgo", "dods", "l4d2", "tf2"} {
		env["SOURCE_GAME"] = name
		if _, err := FromEnv(func(k string) string { return env[k] }); err != nil {
			t.Errorf("%s panel defaults: %v", name, err)
		}
	}
	if strings.Contains(panel.Startup, "{{CUSTOM_PARAMS}}") {
		t.Fatal("panel interpolates untrusted args outside Go argv parser")
	}
}

func TestManagedHostDeliverySkipsContainerCopyAndAdvertisesCapability(t *testing.T) {
	cfg := optionsConfig(t, nil)
	cfg.Mode = "managed"
	var advertised bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["action"] == "prepare" {
			advertised, _ = req["host_delivery"].(bool)
		}
		io.WriteString(w, `{"managed":true,"delivered":true,"cleanup_managed":true,"generation":{"identity":{"game":"css","app_id":"232330","branch":"public","runtime":"linux-auto"},"key":"generations/css/public/linux-auto/fixture"}}`)
	}))
	defer srv.Close()
	e := Egg{Config: cfg, Installer: &installRecorder{}, Node: srv.Client(), NodeEndpoint: srv.URL, Progress: io.Discard}
	p, err := e.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !advertised || !p.Managed || p.Generation != "generations/css/public/linux-auto/fixture" {
		t.Fatalf("host delivery integration lost: %+v capability=%v", p, advertised)
	}
}

func TestHostDeliveryStillRejectsInvalidGenerationPath(t *testing.T) {
	cfg := optionsConfig(t, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"managed":true,"delivered":true,"generation":{"identity":{"game":"css","app_id":"232330","branch":"public","runtime":"linux-auto"},"key":"../../escape"}}`)
	}))
	defer srv.Close()
	e := Egg{Config: cfg, Node: srv.Client(), NodeEndpoint: srv.URL, Progress: io.Discard}
	if _, err := e.Prepare(context.Background()); err == nil {
		t.Fatal("accepted traversal generation")
	}
}

func TestRunRejectsSymlinkedConfigAndLogsBeforeWritingOutside(t *testing.T) {
	for _, name := range []string{".source", ".source/logs"} {
		t.Run(name, func(t *testing.T) {
			cfg := optionsConfig(t, nil)
			outside := t.TempDir()
			os.MkdirAll(filepath.Dir(filepath.Join(cfg.Dir, name)), 0755)
			if err := os.Symlink(outside, filepath.Join(cfg.Dir, name)); err != nil {
				t.Fatal(err)
			}
			e := Egg{Config: cfg, Installer: &installRecorder{}}
			if err := e.Run(context.Background()); err == nil {
				t.Fatal("accepted symlinked server state")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("wrote outside server: %v", entries)
			}
		})
	}
}

func TestPrepareNegotiatesOldNodeOnceWithoutDowngradingDeliveryErrors(t *testing.T) {
	for _, tc := range []struct {
		name, message     string
		status, wantCalls int
		wantError         bool
	}{{"old node", "invalid protocol or game identity", 400, 2, false}, {"delivery error", "selected push_method requires an egg image with host delivery support", 500, 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req map[string]any
				json.NewDecoder(r.Body).Decode(&req)
				if calls == 1 {
					if req["host_delivery"] != true {
						t.Error("first request did not advertise host delivery")
					}
					w.WriteHeader(tc.status)
					json.NewEncoder(w).Encode(map[string]string{"error": tc.message})
					return
				}
				if _, present := req["host_delivery"]; present {
					t.Error("legacy request still has unsupported capability field")
				}
				io.WriteString(w, `{}`)
			}))
			defer srv.Close()
			cfg := optionsConfig(t, map[string]string{"CLEANUP_ENABLED": "1"})
			writeOptionFile(t, cfg.Dir, ".source/configs/cleanup.json", `{"rules":[{"name":"demos","directories":["."],"patterns":["*.dem"],"hours":0}]}`)
			writeOptionFile(t, cfg.Dir, "keep.dem", "legacy node owns cleanup decision")
			e := Egg{Config: cfg, Installer: &installRecorder{}, Node: srv.Client(), NodeEndpoint: srv.URL, Progress: io.Discard}
			_, err := e.Prepare(context.Background())
			if (err != nil) != tc.wantError {
				t.Fatalf("prepare error=%v", err)
			}
			if calls != tc.wantCalls {
				t.Fatalf("node requests=%d,want %d", calls, tc.wantCalls)
			}
			if _, err := os.Stat(filepath.Join(cfg.Dir, "keep.dem")); err != nil {
				t.Fatal("ran fallback cleanup without knowing legacy node ownership")
			}
		})
	}
}

func TestStandardLaunchPassesMetadataLiterallyWithoutShellEvaluation(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"SERVER_HOSTNAME": "server $(touch unexpected)", "CUSTOM_PARAMS": `+sv_tags "two words"`})
	// Valve wrappers can reconstruct a command string and split quoted values.
	writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\nexec ./srcds_linux64 $*\n")
	writeOptionFile(t, cfg.Dir, "srcds_linux64", "#!/bin/sh\nprintf '%s\\n' \"$@\" > received-args\n")
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_linux64"), 0755); err != nil {
		t.Fatal(err)
	}
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(filepath.Join(cfg.Dir, "received-args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "+hostname\nserver $(touch unexpected)\n") || !strings.Contains(string(args), "+sv_tags\ntwo words\n") {
		t.Fatalf("metadata was not passed as literal argv: %q", args)
	}
	if _, err := os.Stat(filepath.Join(cfg.Dir, "unexpected")); !os.IsNotExist(err) {
		t.Fatal("evaluated metadata as shell code")
	}
}

func TestDirectArgvLaunchUsesEachGameArchitecture(t *testing.T) {
	for _, tc := range []struct{ game, binary string }{{"css", "./srcds_linux64"}, {"csgo", "./srcds_linux64"}, {"dods", "./srcds_linux64"}, {"l4d2", "./srcds_linux"}, {"tf2", "./srcds_linux64"}} {
		t.Run(tc.game, func(t *testing.T) {
			cfg := optionsConfig(t, map[string]string{"SOURCE_GAME": tc.game, "CUSTOM_PARAMS": `+sv_tags "test server"`, "CONSOLE_LOG_LEVEL": "ERROR"})
			for _, name := range []string{"srcds_linux", "srcds_linux64"} {
				writeOptionFile(t, cfg.Dir, name, "#!/bin/sh\nprintf '%s\\n' \"$0\" > native-binary\nprintf '%s\\n' \"$@\" > native-args\n")
				if err := os.Chmod(filepath.Join(cfg.Dir, name), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.game == "csgo" {
				writeOptionFile(t, cfg.Dir, "csgo/steam.inf", "appID=730\n")
			}
			e := Egg{Config: cfg, Installer: &installRecorder{}}
			if err := e.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile(filepath.Join(cfg.Dir, "native-binary"))
			if err != nil || string(binary) != tc.binary+"\n" {
				t.Fatalf("wrong architecture %q,%v", binary, err)
			}
			args, err := os.ReadFile(filepath.Join(cfg.Dir, "native-args"))
			if err != nil || !strings.Contains(string(args), "+sv_tags\ntest server\n") {
				t.Fatalf("lost quoted params %q,%v", args, err)
			}
		})
	}
}

func TestCleanupKeepsOpenFilesAndHonorsOnStartSetting(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"CLEANUP_ENABLED": "1"})
	writeOptionFile(t, cfg.Dir, ".source/configs/cleanup.json", `{"on_start":false,"keep_open":true,"rules":[{"name":"demos","directories":["."],"patterns":["*.dem"],"hours":0}]}`)
	writeOptionFile(t, cfg.Dir, "closed.dem", "old")
	writeOptionFile(t, cfg.Dir, "open.dem", "active")
	held, err := os.Open(filepath.Join(cfg.Dir, "open.dem"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	e := Egg{Config: cfg, Installer: &installRecorder{}, Progress: io.Discard}
	if _, err = e.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(cfg.Dir, "closed.dem")); err != nil {
		t.Fatal("ignored on_start=false")
	}
	writeOptionFile(t, cfg.Dir, ".source/configs/cleanup.json", `{"on_start":true,"keep_open":true,"rules":[{"name":"demos","directories":["."],"patterns":["*.dem"],"hours":0}]}`)
	if _, err = e.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(cfg.Dir, "closed.dem")); !os.IsNotExist(err) {
		t.Fatal("did not remove eligible closed demo")
	}
	if _, err = os.Stat(filepath.Join(cfg.Dir, "open.dem")); err != nil {
		t.Fatal("removed open file")
	}
}
