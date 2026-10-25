package content

import (
	"context"
	"github.com/edgegamers/source-egg/internal/game"
	"os"
	"testing"
	"time"
)

// Opt-in Steam metadata check; does not install any retail game.
func TestSteamBuildMetadata(t *testing.T) {
	if os.Getenv("SOURCE_EGG_STEAM_TEST") != "1" {
		t.Skip("explicit Steam metadata test only")
	}
	client := &Steam{Executable: "/opt/steamcmd/steamcmd.sh"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, p := range game.All() {
		t.Run(p.Key, func(t *testing.T) {
			id, _ := p.Identity("public")
			build, err := client.RemoteBuild(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			t.Log(p.Key, build)
		})
	}
}
