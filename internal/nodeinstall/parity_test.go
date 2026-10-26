package nodeinstall

import (
	"bytes"
	"testing"
)

func TestWizardEnablesOperationalModulesAndCalendarPasswords(t *testing.T) {
	c := defaults(t.TempDir())
	c.Shared = false
	c.FrameworkCache = false
	c.Observability.Crashes.Enabled = false
	var out bytes.Buffer
	answers := &promptAnswers{out: &out, values: map[string]string{
		"Join-password format": "eff", "Words per join-password phrase": "6", "Password rotation schedule": "monthly",
		"Archive server console logs on this node": "yes", "Console archive retention days": "7",
		"Collect crash bundles and nearby evidence": "yes", "Crash bundle retention days": "14", "Crash bundles retained per server": "5",
		"Enable backups": "yes", "Hour of the daily backup run": "3", "Days to keep a backup": "7", "Backups to keep per server": "4",
		"Enable cleanup": "yes", "Hours between cleanup passes": "12",
		"Enable metrics": "yes", "Prometheus listener": "127.0.0.1:9152",
		"Enable maintenance": "yes", "Keep host maintenance in dry-run mode": "yes",
		"Self-update automatically": "yes", "Minutes between node update checks": "60",
	}}
	got, e := Configure(c, answers, &out)
	if e != nil {
		t.Fatal(e, out.String())
	}
	if !got.Operations.Backup.Enabled || got.Operations.Backup.Hour != 3 || !got.Operations.Cleanup.Enabled || !got.Operations.Maintenance.Enabled || !got.Operations.Maintenance.DryRun || !got.Metrics.Enabled || got.Metrics.Listen != "127.0.0.1:9152" || !got.SelfUpdate.Enabled || got.SelfUpdate.CheckMinutes != 60 || !got.Observability.Logs.Enabled || got.PasswordFormat != "eff" || got.PasswordMonths != 1 || got.PasswordWords != 6 {
		t.Fatalf("choices not retained: %+v", got)
	}
}
