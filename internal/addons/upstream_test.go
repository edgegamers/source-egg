package addons

import (
	"context"
	"os"
	"testing"
	"time"
)

// Explicit opt-in network check; normal unit/integration tests are offline.
func TestOfficialLinuxPackages(t *testing.T) {
	if os.Getenv("SOURCE_EGG_UPSTREAM_TEST") != "1" {
		t.Skip("set SOURCE_EGG_UPSTREAM_TEST=1 to fetch official framework archives")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := NewCache(t.TempDir(), nil)
	for _, name := range []string{"metamod", "sourcemod"} {
		t.Run(name, func(t *testing.T) {
			p, e := c.Resolve(ctx, name, "")
			if e != nil {
				t.Fatal(e)
			}
			archive, e := c.Fetch(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			t.Log(name, p.Version)
			if e = Deploy(archive, t.TempDir(), "cstrike", name, p.Version); e != nil {
				t.Fatal(e)
			}
		})
	}
}
