package nodeinstall

import (
	"bytes"
	"strings"
	"testing"
)

func TestWizardEnablesOperationalModulesAndCalendarPasswords(t *testing.T) {
	c := defaults(t.TempDir())
	c.Shared = false
	c.FrameworkCache = false
	c.Observability.Crashes.Enabled = false
	answers := strings.Join([]string{
		"no", "no", "empty", "60", "60", "30", "game", "168", "", "",
		"eff", "6", "1",
		"yes", "", "7",
		"yes", "", "14", "5",
		"yes", "", "3", "7", "4",
		"yes", "12",
		"yes", "127.0.0.1:9152",
		"yes", "yes",
		"yes", "60",
	}, "\n") + "\n"
	var out bytes.Buffer
	got, e := Configure(c, strings.NewReader(answers), &out)
	if e != nil {
		t.Fatal(e, out.String())
	}
	if !got.Operations.Backup.Enabled || got.Operations.Backup.Hour != 3 || !got.Operations.Cleanup.Enabled || !got.Operations.Maintenance.Enabled || !got.Operations.Maintenance.DryRun || !got.Metrics.Enabled || got.Metrics.Listen != "127.0.0.1:9152" || !got.SelfUpdate.Enabled || got.SelfUpdate.CheckMinutes != 60 || !got.Observability.Logs.Enabled || got.PasswordFormat != "eff" || got.PasswordMonths != 1 || got.PasswordWords != 6 {
		t.Fatalf("choices not retained: %+v", got)
	}
}
