package node

import (
	"context"
	"log"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
)

func (d *Daemon) checkUpdates(ctx context.Context) {
	if !d.updating.TryLock() {
		return
	}
	defer d.updating.Unlock()
	if !d.Config.Shared {
		return
	}
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	for _, s := range d.servers {
		servers = append(servers, s)
	}
	d.mu.Unlock()
	identities := map[game.Identity]bool{}
	for _, s := range servers {
		if s.Env["SOURCE_MODE"] != "standalone" {
			identities[s.Identity] = true
		}
	}
	for id := range identities {
		before, _ := d.Store.Current(id)
		g, e := d.Store.Ensure(ctx, id)
		if e != nil {
			log.Printf("%s update check failed: %v", id.Game, e)
			d.notice(ctx, "game.update.failed", Server{Identity: id}, nil)
			continue
		}
		if g.Key != before.Key {
			d.notice(ctx, "game.update", Server{Identity: id}, map[string]any{"from": before.BuildID, "to": g.BuildID})
		}
	}
	d.reconcile()
}

// Reconcile persisted server references even when no new Steam build appeared.
func (d *Daemon) reconcile() {
	if !d.Config.Shared {
		return
	}
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	for _, s := range d.servers {
		servers = append(servers, s)
	}
	d.mu.Unlock()
	changed := false
	deferred := []Server{}
	for _, s := range servers {
		if !s.Running || s.Env["SOURCE_MODE"] == "standalone" {
			continue
		}
		g, err := d.desired(s)
		if err != nil {
			continue
		}
		d.mu.Lock()
		ref := d.refs[s.UUID]
		if ref.Current == "" {
			d.mu.Unlock()
			continue
		}
		if ref.Current == g.Key {
			if _, ok := d.pending[s.UUID]; ok {
				delete(d.pending, s.UUID)
				changed = true
			}
			d.mu.Unlock()
			continue
		}
		if p, ok := d.pending[s.UUID]; !ok || p.Generation != g.Key {
			d.pending[s.UUID] = pending{Generation: g.Key, Since: time.Now()}
			if d.Config.Restart.Mode != "immediate" {
				deferred = append(deferred, s)
			}
			changed = true
		}
		d.mu.Unlock()
	}
	if changed {
		d.mu.Lock()
		if err := d.saveRefs(); err != nil {
			log.Printf("pending state save failed: %v", err)
		}
		d.mu.Unlock()
	}
	for _, s := range deferred {
		d.notice(context.Background(), "restart.deferred", s, map[string]any{"policy": d.Config.Restart.Mode})
	}
}
func (d *Daemon) rollout(ctx context.Context) {
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	for _, s := range d.servers {
		servers = append(servers, s)
	}
	d.mu.Unlock()
	for _, s := range servers {
		if !s.Running {
			continue
		}
		d.mu.Lock()
		p, ok := d.pending[s.UUID]
		st := d.states[s.UUID]
		d.mu.Unlock()
		if !ok {
			continue
		}
		current, e := d.desired(s)
		if e != nil || current.Key != p.Generation {
			continue
		}
		players := -1
		if st.Boot == s.Boot && time.Since(st.UpdatedAt) < 2*time.Minute {
			players = st.Players
		}
		if !p.RestartedAt.IsZero() && time.Since(p.RestartedAt) < 5*time.Minute {
			continue
		}
		if !Due(d.Config.Restart, p.Since, time.Now(), players) {
			continue
		}
		if players != 0 && d.Config.Restart.WarnSeconds > 0 {
			if p.WarnedAt.IsZero() {
				p.WarnedAt = time.Now()
				d.mu.Lock()
				d.pending[s.UUID] = p
				_ = d.saveRefs()
				d.mu.Unlock()
				continue
			}
			if time.Since(p.WarnedAt) < time.Duration(d.Config.Restart.WarnSeconds)*time.Second {
				continue
			}
		}
		if e = d.restart(ctx, s); e != nil {
			log.Printf("restart deferred for %s: %v", s.UUID, e)
			continue
		}
		p.RestartedAt = time.Now()
		d.mu.Lock()
		d.pending[s.UUID] = p
		_ = d.saveRefs()
		d.mu.Unlock()
		d.notice(ctx, "server.restart", s, map[string]any{"build": current.BuildID})
	}
}
