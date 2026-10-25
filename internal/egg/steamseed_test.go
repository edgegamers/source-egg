package egg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSteamClientSeedIsWritableAndPreservesSessions(t *testing.T) {
	seed := t.TempDir()
	volume := t.TempDir()
	os.MkdirAll(filepath.Join(seed, "linux32"), 0755)
	os.WriteFile(filepath.Join(seed, "steamcmd.sh"), []byte("#!/bin/sh\n"), 0555)
	os.WriteFile(filepath.Join(seed, "linux32/steamclient.so"), []byte("client"), 0444)
	if err := SeedSteamCMD(seed, volume); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(volume, ".source/steamcmd")
	if err := os.WriteFile(filepath.Join(client, "session"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SeedSteamCMD(seed, volume); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(client, "session"))
	if string(b) != "keep" {
		t.Fatal("destroyed session")
	}
	if err := os.WriteFile(filepath.Join(client, "steamcmd.sh"), []byte("updated"), 0755); err != nil {
		t.Fatal("client not writable", err)
	}
}
