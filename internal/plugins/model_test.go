package plugins

import "testing"

func TestRecipeRejectsAmbiguousOrUnsafeInputs(t *testing.T) {
	p := Plugin{ID: "demo", Source: Source{Provider: "github", Repository: "owner/demo"}, Asset: "demo-linux*.zip", Mappings: []Mapping{{From: "bundle/addons", To: "addons"}}}
	if err := ValidatePlugin(p); err != nil {
		t.Fatal(err)
	}
	cases := []Plugin{p, p, p, p, p, p, p}
	cases[0].ID = "../demo"
	cases[1].Source = Source{Provider: "github", Repository: "owner/repo/extra"}
	cases[2].Asset = ""
	cases[3].Mappings = []Mapping{{From: "../escape", To: "addons"}}
	cases[4].Mappings = []Mapping{{From: ".", To: "../../escape"}}
	cases[5].Source = Source{Provider: "gitlab", Repository: "group/repo", URL: "https://token@host.test"}
	cases[6].Preserve = []string{"../escape"}
	for i, p := range cases {
		if ValidatePlugin(p) == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}
func TestProtectedTargetsAndManifestDefaults(t *testing.T) {
	for _, p := range []string{"cfg/server.cfg", "addons/sourcemod/configs/admins.cfg", "addons/sourcemod/data/a.txt", "addons/sourcemod/logs/x", "addons/metamod.vdf", "addons/custom/settings.json"} {
		if !Protected(p, []string{"addons/custom"}) {
			t.Fatalf("not protected: %s", p)
		}
	}
	if Protected("addons/sourcemod/gamedata/a.txt", nil) {
		t.Fatal("gamedata must update")
	}
	for _, p := range []string{"../escape", ".source/token", "srcds_linux", "addons/../srcds_linux", "addons/x\\y", "addons/x\n"} {
		if ValidTarget(p) {
			t.Fatalf("unsafe target %q", p)
		}
	}
	m := Manifest{Version: 1}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.CheckIntervalHours != 6 {
		t.Fatalf("default interval %d", m.CheckIntervalHours)
	}
	m.Plugins = []Plugin{{ID: "x"}, {ID: "x"}}
	if m.Validate() == nil {
		t.Fatal("invalid plugins accepted")
	}
}
