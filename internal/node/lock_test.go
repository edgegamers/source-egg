package node

import "testing"

func TestOnlyOneDaemonOwnsNodeState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.lock()
	if err != nil {
		t.Fatal(err)
	}
	if second, err := d.lock(); err == nil {
		second.Close()
		first.Close()
		t.Fatal("two daemons acquired the same state")
	}
	first.Close()
	last, err := d.lock()
	if err != nil {
		t.Fatal(err)
	}
	last.Close()
}
