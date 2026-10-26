package node

import (
	"context"
	"errors"
	"testing"
)

func TestFrameworkPolicyPrecedenceAndPersistentLatestException(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	verify := func(_ context.Context, name, pin string) error {
		if pin == "missing" {
			return errors.New("not found")
		}
		return nil
	}
	if e = d.setFrameworkPin(context.Background(), "metamod", "fleet-v1", "", verify); e != nil {
		t.Fatal(e)
	}
	s := Server{UUID: "server-a", Env: map[string]string{}}
	if pin := d.frameworkPin(s, "metamod"); pin != "fleet-v1" {
		t.Fatal(pin)
	}
	if e = d.setFrameworkPin(context.Background(), "metamod", "server-v2", s.UUID, verify); e != nil {
		t.Fatal(e)
	}
	if pin := d.frameworkPin(s, "metamod"); pin != "server-v2" {
		t.Fatal(pin)
	}
	s.Env["METAMOD_VERSION"] = "panel-v3"
	if pin := d.frameworkPin(s, "metamod"); pin != "panel-v3" {
		t.Fatal(pin)
	}
	delete(s.Env, "METAMOD_VERSION")
	if e = d.setFrameworkPin(context.Background(), "metamod", "latest", s.UUID, verify); e != nil {
		t.Fatal(e)
	}
	again, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if pin := again.frameworkPin(s, "metamod"); pin != "" {
		t.Fatal("latest exception lost", pin)
	}
	if e = again.setFrameworkPin(context.Background(), "metamod", "missing", "", verify); e == nil {
		t.Fatal("missing release pin accepted")
	}
	if pin := again.frameworkPin(Server{UUID: "other"}, "metamod"); pin != "fleet-v1" {
		t.Fatal("failed pin changed policy", pin)
	}
}

func TestServerPrefixMustBeUniqueAndNeverUsesPartialVolume(t *testing.T) {
	d := &Daemon{servers: map[string]Server{"abcd-1": {UUID: "abcd-1"}, "abcd-2": {UUID: "abcd-2"}}}
	if _, e := d.selectServer("abcd"); e == nil {
		t.Fatal("ambiguous selection accepted")
	}
	if s, e := d.selectServer("abcd-1"); e != nil || s.UUID != "abcd-1" {
		t.Fatal(s, e)
	}
	if _, e := d.selectServer("../../etc"); e == nil {
		t.Fatal("invalid selector accepted")
	}
}
