package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyConfigGainsOptionalModulesWithoutChangingPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"state_dir":"/tmp/sourcenode-test","password_scope":"game","webhook":"https://discord.com/api/webhooks/id/token"}`), 0600)
	c, e := LoadConfig(path)
	if e != nil {
		t.Fatal(e)
	}
	if c.Operations.Maintenance.Enabled || !c.Operations.Maintenance.DryRun || c.Metrics.Enabled || c.SelfUpdate.Enabled {
		t.Fatal("optional host modules became active")
	}
	effective := c.effectiveObservability()
	if !effective.Alerts.Enabled || effective.Alerts.Webhook != c.Webhook || effective.Crashes.Dir != "/tmp/sourcenode-test/crashes" {
		t.Fatal(effective)
	}
	if c.PasswordScope != "game" {
		t.Fatal("scope changed")
	}
}

func TestPrivateModuleStorageCannotBeMountedIntoServers(t *testing.T) {
	c := DefaultConfig()
	c.StateDir = t.TempDir()
	c.SharedDir = t.TempDir()
	c.CacheDir = t.TempDir()
	c.Observability.Logs.Enabled = true
	c.Observability.Logs.Dir = filepath.Join(c.CacheDir, "logs")
	if c.Validate() == nil {
		t.Fatal("private console logs allowed in public framework mount")
	}
	c.Observability.Logs.Dir = ""
	c.Operations.Backup.Dir = filepath.Join(c.SharedDir, "backups")
	if c.Validate() == nil {
		t.Fatal("private backups allowed in public game mount")
	}
}

func TestDeferredUpdateAndFrameworkReceiptsEmitNotices(t *testing.T) {
	c := DefaultConfig()
	c.StateDir = t.TempDir()
	c.SharedDir = t.TempDir()
	c.Webhook = "https://discord.com/api/webhooks/id/token"
	g := published(t, c.SharedDir, "200")
	d, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	s := Server{UUID: "server-a", Identity: g.Identity, Running: true, Volume: t.TempDir()}
	d.servers[s.UUID] = s
	d.refs[s.UUID] = Reference{Current: "generations/css/public/linux-auto/100"}
	d.reconcile()
	if d.Observation.Status().AlertsQueued != 1 {
		t.Fatal("deferred restart notice missing")
	}
	os.MkdirAll(filepath.Join(s.Volume, ".source"), 0700)
	os.WriteFile(filepath.Join(s.Volume, ".source/framework-metamod.json"), []byte(`{"version":"v1"}`), 0600)
	d.handle(context.Background(), s, Request{Action: "state", Ready: true, Players: 1})
	if d.Observation.Status().AlertsQueued != 2 {
		t.Fatal("installed framework notice missing")
	}
	d.handle(context.Background(), s, Request{Action: "state", Ready: true, Players: 1})
	if d.Observation.Status().AlertsQueued != 2 {
		t.Fatal("framework notice repeated")
	}
}

func TestBrokerLogQueueCannotAddressAnotherServer(t *testing.T) {
	c := DefaultConfig()
	c.StateDir = t.TempDir()
	c.Observability.Logs.Enabled = true
	d, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Observation.Run(ctx); close(done) }()
	defer func() { cancel(); <-done; d.Observation.Close() }()
	s := Server{UUID: "own-server", Volume: t.TempDir(), Env: map[string]string{"ENABLE_LOG_FILES": "1"}}
	if _, e = d.handle(ctx, s, Request{Action: "logs", Console: []string{"hello"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = d.handle(ctx, s, Request{Action: "logs", Console: make([]string, 33)}); e == nil {
		t.Fatal("unbounded batch accepted")
	}
	deadline := time.Now().Add(time.Second)
	for {
		logs, e := d.Observation.ListLogs(ctx, s.UUID)
		if e == nil && len(logs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("logs were not archived", e)
		}
		time.Sleep(time.Millisecond)
	}
	if files, _ := d.Observation.ListLogs(ctx, "other-server"); len(files) != 0 {
		t.Fatal("log attributed to wrong server")
	}
}
