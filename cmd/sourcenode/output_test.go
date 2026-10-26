package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestStatusOutputUsesOperatorFieldsAndSanitizesNames(t *testing.T) {
	data := []byte(`{"servers":[{"server":"12345678-1111-2222-3333-444444444444","game":"css","running":true,"state":{"name":"Club\u001b[2J\nforged","players":12,"map":"de_dust2","ready":true,"password":"hidden-secret"},"reference":{"current":"generations/css/public/linux-auto/200"},"pending":"generations/css/public/linux-auto/201"}],"modules":{"version":"v2","channel":"stable","metrics_enabled":true,"framework_cache":true,"self_update_enabled":false,"observability":{"alerts_queued":2},"operations":{}},"webhook":"hidden-hook"}`)
	var out bytes.Buffer
	if err := renderResponse(&out, "status", data, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, expected := range []string{"Servers", "12345678", "css", "12", "de_dust2", "running", "201", "v2"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %q in %s", expected, got)
		}
	}
	for _, forbidden := range []string{"\x1b", "\r", "hidden-secret", "hidden-hook", `"servers"`, "12345678-1111-2222-3333-444444444444"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unsafe or raw output %q", forbidden)
		}
	}
}

func TestInventoryOutputsShowActionableRows(t *testing.T) {
	for _, tc := range []struct {
		command, body string
		want          []string
	}{
		{"backups", `[{"server_id":"12345678-aaaa","game":"css","time":"2026-10-26T05:00:00Z","files":42,"bytes":2048,"path":"/private/backup.tgz"}]`, []string{"Backups", "12345678", "42", "2.0 KiB", "2026-10-26"}},
		{"logs", `[{"uuid":"12345678-aaaa","name":"console.log.gz","date":"2026-10-26T00:00:00Z","bytes":1024,"compressed":true}]`, []string{"Logs", "console.log.gz", "1.0 KiB"}},
		{"crashes", `[{"uuid":"12345678-aaaa","time":"2026-10-26T05:00:00Z","game":"css","server_name":"Club","map":"de_dust2","exit_code":139,"bytes":1024}]`, []string{"Crashes", "139", "de_dust2"}},
		{"cleanup", `[{"server_id":"12345678-aaaa","files":4,"bytes":2048,"skipped":2,"per_rule":{"logs":{"files":4,"bytes":2048}}}]`, []string{"Cleanup", "4", "2.0 KiB", "logs"}},
		{"maintenance", `[{"task":"journal","dry":true,"items":5,"bytes":4096,"note":"would vacuum","error":""}]`, []string{"Maintenance", "journal", "preview", "4.0 KiB", "would vacuum"}},
		{"pins", `{"policy":{"fleet":{"sourcemod":"v1.2"},"servers":{}},"servers":[{"Server":"12345678-aaaa","Game":"css","Name":"Club","Installed":{"sourcemod":"v1.1"},"Selected":{"sourcemod":"v1.2"}}]}`, []string{"Fleet pins", "sourcemod", "v1.2", "v1.1", "12345678"}},
		{"history", `[{"identity":{"game":"css","branch":"public"},"key":"generations/css/public/linux-auto/200","build_id":"200"}]`, []string{"Game generations", "200", "generations/css/public/linux-auto/200"}},
		{"doctor", `{"checks":{"docker":"available","steamcmd":{"path":"/opt/steamcmd.sh","executable":true},"wings":"available","reboot_required":false,"state_disk":{"available_bytes":2048,"total_bytes":4096},"mounts":{"12345678-aaaa":{"socket_read_only":true,"shared_read_only":true,"cache_read_only":false}},"crashes_last_24h":1},"modules":{}}`, []string{"Doctor", "Docker", "available", "2.0 KiB", "Mounts", "12345678"}},
		{"metrics", `{"Version":"v1","Channel":"stable","Host":{"CPU":12.5,"Memory":1024,"Limit":2048},"Servers":[{"ID":"12345678-aaaa","Name":"Club","Game":"css","Players":3,"Running":true,"Sample":{"cpu_percent":25,"memory_bytes":1024}}]}`, []string{"Metrics", "12.5%", "12345678", "25.0%"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var out bytes.Buffer
			if err := renderResponse(&out, tc.command, []byte(tc.body), false); err != nil {
				t.Fatal(err)
			}
			for _, expected := range tc.want {
				if !strings.Contains(out.String(), expected) {
					t.Fatalf("missing %q in %s", expected, out.String())
				}
			}
		})
	}
}

func TestJSONOutputPreservesResponseSchema(t *testing.T) {
	original := []byte(`{"servers":[],"modules":{"version":"v2"},"future_field":{"large":1234567890123456789}}`)
	var out bytes.Buffer
	if err := renderResponse(&out, "status", original, true); err != nil {
		t.Fatal(err)
	}
	var before, after any
	decode := func(raw []byte, target *any) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	decode(original, &before)
	decode(out.Bytes(), &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("JSON response schema changed")
	}
	out.Reset()
	if err := renderResponse(&out, "prune", []byte("pruned generations\n"), true); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatal("text command did not produce JSON")
	}
}

func TestPasswordRevealIsExplicitAndCrashTextIsTerminalSafe(t *testing.T) {
	body := []byte(`{"password":"explicit-secret","revision":"r1","generated_at":"2026-10-26T00:00:00Z","next_at":"2026-11-26T00:00:00Z"}`)
	var out bytes.Buffer
	if err := renderResponse(&out, "password", body, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "explicit-secret") || !strings.Contains(out.String(), "Next rotation") {
		t.Fatal("explicit reveal missing password or schedule")
	}
	out.Reset()
	if err := renderResponse(&out, "unknown", body, false); err == nil {
		t.Fatal("unknown response dumped arbitrary secrets")
	}
	out.Reset()
	if err := renderResponse(&out, "crash", []byte("first\nsecond\x1b[2J\r\n"), false); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\r") || !strings.Contains(out.String(), "first\nsecond") {
		t.Fatal("crash text controls or line breaks mishandled")
	}
}

func TestGlobalOutputFlagsWorkBeforeOrAfterCommands(t *testing.T) {
	for _, args := range [][]string{{"--json", "status"}, {"status", "--json"}, {"--config", "/tmp/config.json", "status", "--json"}, {"status", "--json", "--config=/tmp/config.json"}} {
		options, err := parseCLIArgs(args)
		if err != nil || !options.JSON || !reflect.DeepEqual(options.Args, []string{"status"}) {
			t.Fatalf("%v: %+v %v", args, options, err)
		}
	}
	options, err := parseCLIArgs([]string{"--help"})
	if err != nil || !options.Help {
		t.Fatal("help requires node config")
	}
}

func TestShortServerSelectionRejectsAmbiguity(t *testing.T) {
	data := []byte(`{"servers":[{"server":"12345678-one"},{"server":"12345678-two"},{"server":"87654321-three"}]}`)
	if got, err := selectServerID(data, "87654321"); err != nil || got != "87654321-three" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := selectServerID(data, "12345678-one"); err != nil || got != "12345678-one" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := selectServerID(data, "12345678"); err == nil {
		t.Fatal("ambiguous prefix accepted")
	}
	if _, err := selectServerID(data, ""); err == nil {
		t.Fatal("empty prefix accepted")
	}
}
