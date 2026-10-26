package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadForkControlsWithoutLosingExplicitDisabledSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"node_channel":"dev","password_enabled":false,"password_cron":"0 4 * * 1","password_timezone":"America/Chicago","password_interval_hours":0,"push_method":"copy","max_workers":3,"auto_restart":false,"steam_validate":true}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	var got map[string]any
	json.Unmarshal(b, &got)
	for k, want := range map[string]any{"password_enabled": false, "password_cron": "0 4 * * 1", "password_timezone": "America/Chicago", "push_method": "copy", "max_workers": float64(3), "auto_restart": false, "steam_validate": true} {
		if got[k] != want {
			t.Errorf("%s: got %v, want %v", k, got[k], want)
		}
	}
}

func TestLegacyConfigKeepsExistingOptionalModulesDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"password_interval_hours":24}`), 0600)
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Operations.Backup.Enabled || c.Operations.Cleanup.Enabled || c.Operations.Maintenance.Enabled || c.Metrics.Enabled || c.SelfUpdate.Enabled {
		t.Fatal("upgrade enabled optional work")
	}
	if c.PasswordIntervalHours != 24 {
		t.Fatal("lost saved schedule")
	}
}

func TestFreshInstallDefaultsDoNotChangeLegacyPasswordOrModules(t *testing.T) {
	fresh := FreshConfig()
	legacy := DefaultConfig()
	if err := fresh.Validate(); err != nil {
		t.Fatal(err)
	}
	if !fresh.Operations.Backup.Enabled || !fresh.Operations.Cleanup.Enabled || !fresh.Operations.Maintenance.Enabled || !fresh.Operations.Maintenance.DryRun || !fresh.Metrics.Enabled || !fresh.SelfUpdate.Enabled || !fresh.Observability.Logs.Enabled {
		t.Fatal("fresh modules do not follow installer defaults")
	}
	if fresh.PasswordEnabled || fresh.PasswordScope != "server" || fresh.PasswordFormat != "eff" || fresh.PasswordMonths != 1 || fresh.PasswordIntervalHours != 0 {
		t.Fatal("fresh password defaults differ")
	}
	if !legacy.PasswordEnabled || legacy.PasswordScope != "game" || legacy.PasswordFormat != "token" || legacy.PasswordIntervalHours != 168 || legacy.Operations.Backup.Enabled || legacy.Observability.Logs.Enabled {
		t.Fatal("legacy defaults were changed")
	}
}
