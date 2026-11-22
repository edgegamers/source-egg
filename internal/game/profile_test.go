package game

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchUsesSelectedGameAndKeepsSecretsAsArguments(t *testing.T) {
	for _, tt := range []struct{ key, app, dir, client, level, exe string }{
		{"csgo", "740", "csgo", "4465480", "de_dust2", "./srcds_run"},
		{"css", "232330", "cstrike", "240", "de_dust2", "./srcds_run"},
		{"dods", "232290", "dod", "300", "dod_avalanche", "./srcds_run"},
		{"l4d2", "222860", "left4dead2", "550", "c1m1_hotel", "./srcds_run"},
		{"tf2", "232250", "tf", "440", "cp_dustbowl", "./srcds_run_64"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			p, err := Lookup(tt.key)
			if err != nil {
				t.Fatal(err)
			}
			if p.AppID != tt.app || p.ClientAppID != tt.client {
				t.Fatalf("wrong Steam identity: %+v", p)
			}
			args, err := p.Command(Launch{Port: 27015, MaxPlayers: 16, Password: "one two", RCON: "private", Map: tt.level})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(args[:3], []string{tt.exe, "-game", tt.dir}) {
				t.Fatal(args)
			}
			if !strings.Contains(strings.Join(args, "|"), "+sv_password|one two") {
				t.Fatal("password was shell-expanded/split", args)
			}
		})
	}
}

func TestIdentityRefusesGameChangeAndBranchTraversal(t *testing.T) {
	root := t.TempDir()
	p, _ := Lookup("css")
	id, err := p.Identity("public")
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckIdentity(root, id); err != nil {
		t.Fatal(err)
	}
	q, _ := Lookup("tf2")
	other, _ := q.Identity("public")
	if err = CheckIdentity(root, other); err == nil {
		t.Fatal("accepted profile change")
	}
	if _, err = p.Identity("../../other"); err == nil {
		t.Fatal("accepted unsafe branch")
	}
	if _, err = Lookup("source2"); err == nil {
		t.Fatal("accepted unknown game")
	}
	if _, err = filepath.Abs(root); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRejectsConsoleInjectionAndPortZero(t *testing.T) {
	p, _ := Lookup("css")
	for _, l := range []Launch{{Port: 0}, {Port: 27015, Map: "de_dust2;quit"}, {Port: 27015, Password: "bad\nquit"}} {
		if _, err := p.Command(l); err == nil {
			t.Fatalf("accepted %+v", l)
		}
	}
}
