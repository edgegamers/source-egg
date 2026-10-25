package node

import (
	"context"
	"github.com/edgegamers/source-egg/internal/game"
	"log"
)

func (d *Daemon) warmup(ctx context.Context) {
	if !d.Config.Shared {
		return
	}
	for _, key := range d.Config.PrefetchGames {
		if ctx.Err() != nil {
			return
		}
		profile, err := game.Lookup(key)
		if err != nil {
			log.Printf("invalid prefetch game %q", key)
			continue
		}
		id, _ := profile.Identity("public")
		log.Printf("preparing shared %s files before server creation", profile.Name)
		generation, err := d.Store.Ensure(ctx, id)
		if err != nil {
			log.Printf("%s preparation failed: %v", profile.Name, err)
			continue
		}
		log.Printf("shared %s build %s is ready", profile.Name, generation.BuildID)
	}
}
