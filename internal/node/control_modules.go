package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/operations"
	"golang.org/x/sys/unix"
)

func (d *Daemon) commandServer(ctx context.Context, selector string) (Server, error) {
	s, e := d.selectServer(selector)
	if e != nil {
		return s, e
	}
	fresh, e := d.Docker.Inspect(ctx, s.ID, d.Config.Images)
	if e != nil {
		return s, e
	}
	if fresh.UUID != s.UUID || fresh.Identity != s.Identity || fresh.Volume != s.Volume {
		return s, errors.New("server identity changed")
	}
	return fresh, nil
}
func (d *Daemon) recordSelector(selector string) (string, error) {
	if selector == "" {
		return "", nil
	}
	s, e := d.selectServer(selector)
	if e == nil {
		return s.UUID, nil
	}
	if validID.MatchString(selector) && len(selector) >= 32 {
		return selector, nil
	}
	return "", e
}
func (d *Daemon) commandServers(ctx context.Context, selector string) ([]Server, error) {
	if selector != "" {
		s, e := d.commandServer(ctx, selector)
		return []Server{s}, e
	}
	servers, e := d.Docker.Servers(ctx, d.Config.Images)
	if e != nil {
		return nil, e
	}
	out := []Server{}
	for _, s := range servers {
		if s.Running {
			out = append(out, s)
		}
	}
	return out, nil
}
func controlJSON(w http.ResponseWriter, v any, e error) {
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (d *Daemon) moduleControl(mux *http.ServeMux) {
	mux.HandleFunc("GET /modules", func(w http.ResponseWriter, r *http.Request) { controlJSON(w, d.moduleStatus(), nil) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if !d.Config.Metrics.Enabled {
			controlJSON(w, d.sampleMetrics(r.Context()), nil)
		} else {
			controlJSON(w, d.metricsSnapshot(), nil)
		}
	})
	mux.HandleFunc("GET /doctor", func(w http.ResponseWriter, r *http.Request) { controlJSON(w, d.doctor(r.Context()), nil) })
	mux.HandleFunc("GET /pins", func(w http.ResponseWriter, r *http.Request) { controlJSON(w, d.frameworkInventory(), nil) })
	mux.HandleFunc("POST /framework-pin", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		server := ""
		if q.Get("server") != "" {
			s, e := d.selectServer(q.Get("server"))
			if e != nil {
				controlJSON(w, nil, e)
				return
			}
			server = s.UUID
		}
		e := d.setFrameworkPin(r.Context(), q.Get("name"), q.Get("version"), server, func(ctx context.Context, name, pin string) error {
			pkg, e := d.Cache.Resolve(ctx, name, pin)
			if e != nil {
				return e
			}
			if pkg.Version == "" {
				return errors.New("pin resolved no release")
			}
			return nil
		})
		controlJSON(w, d.frameworkInventory(), e)
	})
	mux.HandleFunc("GET /backups", func(w http.ResponseWriter, r *http.Request) {
		id, e := d.recordSelector(r.URL.Query().Get("server"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		items, e := d.Operations.Backups(r.Context(), id)
		controlJSON(w, items, e)
	})
	mux.HandleFunc("POST /backup", func(w http.ResponseWriter, r *http.Request) {
		servers, e := d.commandServers(r.Context(), r.URL.Query().Get("server"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		records := []operations.BackupRecord{}
		for _, s := range servers {
			item, e := d.Operations.Backup(r.Context(), d.operationServer(s))
			if e != nil {
				controlJSON(w, nil, e)
				return
			}
			records = append(records, item)
		}
		controlJSON(w, records, nil)
	})
	mux.HandleFunc("POST /restore", func(w http.ResponseWriter, r *http.Request) {
		s, e := d.commandServer(r.Context(), r.URL.Query().Get("server"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		if s.Running {
			controlJSON(w, nil, errors.New("stop the server before restoring"))
			return
		}
		index, e := positiveIndex(r.URL.Query().Get("index"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		records, e := d.Operations.Backups(r.Context(), s.UUID)
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		sort.Slice(records, func(i, j int) bool { return records[i].Time.After(records[j].Time) })
		if index > len(records) {
			controlJSON(w, nil, errors.New("backup index not found"))
			return
		}
		e = d.Operations.Restore(r.Context(), d.operationServer(s), records[index-1].Path)
		controlJSON(w, map[string]string{"restored": s.UUID}, e)
	})
	mux.HandleFunc("GET /cleanup", func(w http.ResponseWriter, r *http.Request) {
		st := d.Operations.Status()
		if selector := r.URL.Query().Get("server"); selector != "" {
			s, e := d.selectServer(selector)
			if e != nil {
				controlJSON(w, nil, e)
				return
			}
			controlJSON(w, map[string]any{"last": st.Cleanup[s.UUID], "total": st.CleanupTotals[s.UUID]}, nil)
			return
		}
		controlJSON(w, st, nil)
	})
	mux.HandleFunc("POST /cleanup", func(w http.ResponseWriter, r *http.Request) {
		servers, e := d.commandServers(r.Context(), r.URL.Query().Get("server"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		items := []operations.CleanupResult{}
		for _, s := range servers {
			item, e := d.Operations.Cleanup(r.Context(), d.operationServer(s))
			if e != nil {
				controlJSON(w, nil, e)
				return
			}
			items = append(items, item)
		}
		controlJSON(w, items, nil)
	})
	mux.HandleFunc("GET /maintenance", func(w http.ResponseWriter, r *http.Request) { controlJSON(w, d.Operations.Status(), nil) })
	mux.HandleFunc("POST /maintenance", func(w http.ResponseWriter, r *http.Request) {
		apply := r.URL.Query().Get("apply") == "true"
		items, e := d.Operations.Maintain(r.Context(), apply)
		controlJSON(w, items, e)
	})
	mux.HandleFunc("POST /maintenance/setup", func(w http.ResponseWriter, r *http.Request) {
		e := d.Operations.Setup(r.Context())
		controlJSON(w, map[string]string{"setup": "unattended security upgrades configured; automatic reboot disabled"}, e)
	})
	mux.HandleFunc("GET /logs", func(w http.ResponseWriter, r *http.Request) {
		id, e := d.recordSelector(r.URL.Query().Get("server"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		items, e := d.Observation.ListLogs(r.Context(), id)
		controlJSON(w, items, e)
	})
	mux.HandleFunc("GET /crashes", func(w http.ResponseWriter, r *http.Request) {
		id, e := d.recordSelector(r.URL.Query().Get("server"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		items, e := d.Observation.ListCrashes(r.Context(), id)
		controlJSON(w, items, e)
	})
	mux.HandleFunc("GET /crash", func(w http.ResponseWriter, r *http.Request) {
		id, e := d.recordSelector(r.URL.Query().Get("server"))
		if e != nil || id == "" {
			if e == nil {
				e = errors.New("server required")
			}
			controlJSON(w, nil, e)
			return
		}
		index, e := positiveIndex(r.URL.Query().Get("index"))
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		tail, e := d.Observation.Tail(r.Context(), id, index)
		if e != nil {
			controlJSON(w, nil, e)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, tail)
	})
}
func positiveIndex(raw string) (int, error) {
	if raw == "" {
		return 1, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 10000 {
		return 0, errors.New("index must be 1–10000 (1 is newest)")
	}
	return n, nil
}
func (d *Daemon) moduleStatus() any {
	st := d.Operations.Status()
	cfg := d.Config.Operations
	next := map[string]any{}
	now := time.Now()
	if cfg.Backup.Enabled {
		at := time.Date(now.Year(), now.Month(), now.Day(), cfg.Backup.Hour, 0, 0, 0, now.Location())
		if st.LastBackup.In(now.Location()).Format("2006-01-02") == now.Format("2006-01-02") {
			at = at.AddDate(0, 0, 1)
		}
		if at.Before(now) {
			at = now
		}
		next["backup"] = at
	}
	if cfg.Cleanup.Enabled && cfg.Cleanup.EveryHours > 0 {
		at := st.LastCleanup.Add(time.Duration(cfg.Cleanup.EveryHours) * time.Hour)
		if at.Before(now) {
			at = now
		}
		next["cleanup"] = at
	}
	if cfg.Maintenance.Enabled && cfg.Maintenance.EveryHours > 0 {
		at := st.LastMaintenance.Add(time.Duration(cfg.Maintenance.EveryHours) * time.Hour)
		if at.Before(now) {
			at = now
		}
		next["maintenance"] = at
	}
	d.mu.Lock()
	last := d.lastNodeUpdate
	d.mu.Unlock()
	return map[string]any{"operations": st, "next_scheduled": next, "maintenance_dry_run": cfg.Maintenance.DryRun, "observability": d.Observation.Status(), "metrics_enabled": d.Config.Metrics.Enabled, "self_update_enabled": d.Config.SelfUpdate.Enabled, "last_node_update": last, "framework_cache": d.Config.FrameworkCache, "version": d.Version, "channel": d.Config.NodeChannel}
}
func (d *Daemon) doctor(ctx context.Context) any {
	checks := map[string]any{}
	if _, e := d.Docker.prefix(ctx); e != nil {
		checks["docker"] = e.Error()
	} else {
		checks["docker"] = "available"
	}
	if st, e := os.Stat(d.Config.SteamCMD); e != nil || st.IsDir() {
		checks["steamcmd"] = "missing or invalid"
	} else {
		checks["steamcmd"] = map[string]any{"path": d.Config.SteamCMD, "executable": st.Mode()&0111 != 0}
	}
	if _, e := LoadWings(d.Config.WingsConfig); e != nil {
		checks["wings"] = e.Error()
	} else {
		checks["wings"] = "available"
	}
	var disk unix.Statfs_t
	if e := unix.Statfs(d.Config.StateDir, &disk); e == nil {
		checks["state_disk"] = map[string]uint64{"available_bytes": disk.Bavail * uint64(disk.Bsize), "total_bytes": disk.Blocks * uint64(disk.Bsize)}
	}
	_, rebootErr := os.Stat("/var/run/reboot-required")
	checks["reboot_required"] = rebootErr == nil
	if check, e := d.Observation.CheckWebhook(ctx); e != nil {
		checks["alerts"] = d.redact(e.Error())
	} else {
		checks["alerts"] = check
	}
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	for _, s := range d.servers {
		servers = append(servers, s)
	}
	d.mu.Unlock()
	mounts := map[string]any{}
	for _, s := range servers {
		if !s.Running {
			continue
		}
		data, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(s.PID), "mountinfo"))
		if e != nil {
			mounts[s.UUID] = e.Error()
			continue
		}
		shared, socket, cache := false, false, false
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 6 {
				continue
			}
			ro := strings.Contains(","+f[5]+",", ",ro,")
			switch f[4] {
			case SharedMount:
				shared = ro
			case SocketDir:
				socket = ro
			case CacheMount:
				cache = ro
			}
		}
		mounts[s.UUID] = map[string]bool{"socket_read_only": socket, "shared_read_only": shared, "cache_read_only": cache}
	}
	checks["mounts"] = mounts
	bundles, e := d.Observation.ListCrashes(ctx, "")
	if e == nil {
		recent := 0
		for _, b := range bundles {
			if time.Since(b.Time) < 24*time.Hour {
				recent++
			}
		}
		checks["crashes_last_24h"] = recent
	}
	return map[string]any{"checks": checks, "modules": d.moduleStatus()}
}
