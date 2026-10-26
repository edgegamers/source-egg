package nodeinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/operations"
)

// Answer by question so a harmless change in section ordering cannot conceal a
// broken configuration write behind a positional input transcript.
type promptAnswers struct {
	out           *bytes.Buffer
	values        map[string]string
	offset, reads int
}

func (a *promptAnswers) Read(p []byte) (int, error) {
	a.reads++
	if a.reads > 300 {
		return 0, io.EOF
	}
	block := a.out.String()[a.offset:]
	a.offset = a.out.Len()
	// Only answer the active question, not matching labels repeated in Summary.
	if at := strings.LastIndex(block, "\n\n"); at >= 0 {
		block = block[at+2:]
	}
	value := ""
	for label, v := range a.values {
		if strings.Contains(block, label) {
			value = v
			break
		}
	}
	return copy(p, value+"\n"), nil
}

func TestWizardExposesRetentionFiltersAndModuleSettings(t *testing.T) {
	c := defaults(t.TempDir())
	var out bytes.Buffer
	input := &promptAnswers{out: &out, values: map[string]string{
		"Minutes a release answer stays fresh": "25", "Days to keep unused framework downloads": "90",
		"Enable backups": "yes", "Days to keep a backup": "0", "Backups to keep per server": "0",
		"Paths to back up": "cstrike/cfg,cstrike/addons", "Paths to leave out": "*.dem,*.vpk",
		"Enable cleanup": "yes", "Also clean when a server starts": "no", "Leave files the server still has open": "no",
		"demos: retention hours": "48", "logs: retention hours": "72",
		"Enable maintenance": "yes", "Hours between maintenance passes": "24", "Prune dangling Docker images": "no",
		"Days of journal to keep": "9", "Alert when free disk drops below this percent": "20",
		"Enable metrics": "yes", "Seconds between Docker samples": "12",
	}}
	got, err := Configure(c, input, &out)
	if err != nil {
		t.Fatal(err, out.String())
	}
	if got.FrameworkRefreshMinutes != 25 || got.FrameworkKeepDays != 90 {
		t.Fatal("cache settings not offered", got.FrameworkRefreshMinutes, got.FrameworkKeepDays)
	}
	if !got.Operations.Backup.Enabled || got.Operations.Backup.KeepDays != 0 || got.Operations.Backup.KeepCount != 0 || strings.Join(got.Operations.Backup.Include, ",") != "cstrike/cfg,cstrike/addons" {
		t.Fatal("backup choices lost", got.Operations.Backup)
	}
	if got.Operations.Cleanup.OnStart || got.Operations.Cleanup.KeepOpen || got.Operations.Cleanup.Rules[0].Hours != 48 || got.Operations.Cleanup.Rules[1].Hours != 72 {
		t.Fatal("cleanup choices lost", got.Operations.Cleanup)
	}
	if got.Operations.Maintenance.EveryHours != 24 || got.Operations.Maintenance.Images || got.Operations.Maintenance.JournalDays != 9 || got.Operations.Maintenance.DiskWarnPercent != 20 {
		t.Fatal("maintenance choices lost", got.Operations.Maintenance)
	}
	if got.Metrics.SampleSeconds != 12 {
		t.Fatal("sample interval not offered")
	}
}

func TestWizardReviewsSettingsWithoutPrintingWebhookSecrets(t *testing.T) {
	c := defaults(t.TempDir())
	c.Webhook = "https://discord.com/api/webhooks/secret/token"
	c.PasswordWebhook = c.Webhook
	var out bytes.Buffer
	got, err := Configure(c, &promptAnswers{out: &out}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Summary") || !strings.Contains(out.String(), "Install with these settings?") {
		t.Fatal("no settings review before install")
	}
	if strings.Contains(out.String(), "secret/token") {
		t.Fatal("credential printed in review")
	}
	if got.Webhook != c.Webhook || got.PasswordWebhook != c.PasswordWebhook {
		t.Fatal("saved hooks lost")
	}
}

func TestWizardDeclinedSummaryAborts(t *testing.T) {
	c := defaults(t.TempDir())
	var out bytes.Buffer
	_, err := Configure(c, &promptAnswers{out: &out, values: map[string]string{"Install with these settings?": "no"}}, &out)
	if err == nil {
		t.Fatal("declined install accepted")
	}
}

func TestInstallDeclinedReviewDoesNotProvisionOrWrite(t *testing.T) {
	dir := t.TempDir()
	c := defaults(dir)
	var out bytes.Buffer
	called := false
	err := Install(context.Background(), Options{Defaults: c, ConfigPath: filepath.Join(dir, "config.json"), BinaryPath: filepath.Join(dir, "bin/node"), UnitPath: filepath.Join(dir, "node.service"), Input: &promptAnswers{out: &out, values: map[string]string{"Install with these settings?": "no"}}, Output: &out, Provision: func(context.Context, string) error { called = true; return nil }})
	if err == nil || called {
		t.Fatal("cancelled review reached installation", err, called)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled review wrote files", entries, err)
	}
}

func TestWizardPreservesCustomCleanupRulesAndAdvancedSettings(t *testing.T) {
	c := defaults(t.TempDir())
	off := false
	c.Operations.Cleanup.Enabled = true
	c.Operations.Cleanup.Rules = []operations.Rule{{Name: "custom", Description: "My own category", Directories: []string{"tf/logs"}, Patterns: []string{"custom*.log"}, Hours: 123, Recursive: &off, Enabled: &off, DeleteParentDir: true}}
	c.SteamUsername = "saved-user"
	c.SteamPassword = "saved-password"
	c.WingsConfig = "/etc/custom/wings.yml"
	c.Images = []string{"example.org/private/source"}
	c.Observability.Alerts.Enabled = false
	c.Webhook = "https://discord.com/api/webhooks/saved/token"
	wantRules := append([]operations.Rule(nil), c.Operations.Cleanup.Rules...)
	var out bytes.Buffer
	got, err := Configure(c, &promptAnswers{out: &out}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Operations.Cleanup.Rules, wantRules) || got.SteamPassword != c.SteamPassword || got.WingsConfig != c.WingsConfig || !reflect.DeepEqual(got.Images, c.Images) || got.Observability.Alerts.Enabled || got.Webhook != c.Webhook {
		t.Fatal("rerun changed saved advanced settings")
	}
	if strings.Contains(out.String(), "saved-password") || strings.Contains(out.String(), "saved/token") {
		t.Fatal("rerun printed credentials")
	}
}

func TestWizardCronReplacesElapsedSchedule(t *testing.T) {
	c := defaults(t.TempDir())
	c.PasswordMonths = 2
	c.PasswordIntervalHours = 72
	var out bytes.Buffer
	got, err := Configure(c, &promptAnswers{out: &out, values: map[string]string{"Password rotation schedule": "cron", "Password schedule timezone": "America/Chicago", "Password cron expression": "30 3 * * MON-FRI"}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordMonths != 0 || got.PasswordIntervalHours != 0 || got.PasswordCron != "30 3 * * MON-FRI" || got.PasswordTimezone != "America/Chicago" {
		t.Fatal("cron selection did not replace elapsed schedule")
	}
}

func TestInstallSavesReviewedFleetPinsAndKeepsServerExceptions(t *testing.T) {
	dir := t.TempDir()
	c := defaults(dir)
	if err := os.MkdirAll(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	pinPath := filepath.Join(c.StateDir, "framework-pins.json")
	policy := node.FrameworkPolicy{Fleet: map[string]string{"metamod": "1.12.0.1228"}, Servers: map[string]map[string]string{"server-a": {"sourcemod": "latest"}}}
	writePolicy := func() {
		t.Helper()
		data, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(pinPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy()
	source := filepath.Join(dir, "download")
	if err := os.WriteFile(source, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Install(context.Background(), Options{Defaults: c, ConfigPath: filepath.Join(dir, "etc/config.json"), BinaryPath: filepath.Join(dir, "bin/node"), UnitPath: filepath.Join(dir, "systemd/node.service"), Executable: source, Input: &promptAnswers{out: &out, values: map[string]string{"Fleet sourcemod version": "1.13.0.7478"}}, Output: &out, Provision: func(context.Context, string) error {
		// A CLI change during the wizard must not be overwritten by the snapshot.
		policy.Servers["server-b"] = map[string]string{"metamod": "latest"}
		writePolicy()
		return nil
	}, Run: func(context.Context, string, ...string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := loadSetupPins(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Fleet["sourcemod"] != "1.13.0.7478" || saved.Fleet["metamod"] != "1.12.0.1228" || !reflect.DeepEqual(saved.Servers, policy.Servers) {
		t.Fatal("framework policy lost", saved)
	}
}
