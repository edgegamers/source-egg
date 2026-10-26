package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edgegamers/source-egg/internal/node"
)

func fixtureControl(t *testing.T, handler http.Handler) string {
	t.Helper()
	config := node.DefaultConfig()
	config.StateDir = t.TempDir()
	config.SharedDir = t.TempDir()
	config.CacheDir = t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(config.StateDir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close(); listener.Close() })
	return configPath
}

func captureCLI(t *testing.T, args []string) (string, error) {
	t.Helper()
	savedArgs, savedOut := os.Args, os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"sourcenode"}, args...)
	os.Stdout = writer
	defer func() { os.Args = savedArgs; os.Stdout = savedOut; reader.Close(); writer.Close() }()
	runErr := run()
	writer.Close()
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(output), runErr
}

func TestCLIUsesJSONFlagAnywhereWithoutChangingAPI(t *testing.T) {
	body := `{"servers":[],"modules":{"version":"fixture"},"number":1234567890123456789}`
	config := fixtureControl(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	}))
	for _, args := range [][]string{{"--config", config, "--json", "status"}, {"status", "--json", "--config", config}} {
		output, err := captureCLI(t, args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(output) != body {
			t.Fatalf("API output changed: %s", output)
		}
	}
	output, err := captureCLI(t, []string{"status", "--config", config})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Servers") || !strings.Contains(output, "fixture") || json.Valid([]byte(output)) {
		t.Fatalf("expected readable status, got %s", output)
	}
}

func TestCLIResolvesShortIDBeforeExplicitPasswordReveal(t *testing.T) {
	const id = "12345678-1111-2222-3333-444444444444"
	config := fixtureControl(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			io.WriteString(w, `{"servers":[{"server":"`+id+`"}]}`)
		case "/password":
			if r.URL.Query().Get("server") != id {
				http.Error(w, "full identity required", 400)
				return
			}
			io.WriteString(w, `{"password":"explicit-fixture-secret","revision":"one"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	output, err := captureCLI(t, []string{"--config", config, "passwords", "show", "12345678"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "explicit-fixture-secret") {
		t.Fatal("explicit password reveal missing")
	}
}

func TestCLIHelpDoesNotReadConfig(t *testing.T) {
	output, err := captureCLI(t, []string{"--config", "/missing/config.json", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"backups", "maintenance", "cleanup", "self-update", "uninstall", "--json", "stable|beta|dev"} {
		if !strings.Contains(output, command) {
			t.Fatalf("help omitted %s", command)
		}
	}
}

func TestCLIJSONConfirmationKeepsStdoutMachineReadable(t *testing.T) {
	config := fixtureControl(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/restore" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"restored":"12345678"}`)
	}))
	input, err := os.CreateTemp(t.TempDir(), "answer")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err = input.WriteString("yes\n"); err != nil {
		t.Fatal(err)
	}
	if _, err = input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = saved }()
	output, err := captureCLI(t, []string{"--config", config, "restore", "12345678", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(output)) {
		t.Fatalf("confirmation polluted JSON stdout: %q", output)
	}
}
