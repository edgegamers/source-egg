package node

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/edgegamers/source-egg/internal/metrics"
)

func (d *Docker) Stats(ctx context.Context, id string) (metrics.DockerStats, error) {
	var out metrics.DockerStats
	if !validID.MatchString(id) {
		return out, errors.New("invalid container identifier")
	}
	prefix, e := d.prefix(ctx)
	if e != nil {
		return out, e
	}
	e = d.get(ctx, prefix+"/containers/"+url.PathEscape(id)+"/stats?stream=false&one-shot=true", &out)
	return out, e
}
func (d *Daemon) sampleMetrics(ctx context.Context) metrics.Snapshot {
	d.metricsMu.Lock()
	defer d.metricsMu.Unlock()
	now := time.Now()
	if d.metricsPrevious == nil {
		d.metricsPrevious = map[string]metrics.DockerStats{}
	}
	snapshot := metrics.Snapshot{Version: d.Version, Channel: d.Config.NodeChannel, At: now, Uptime: time.Since(d.startedAt).Seconds(), Servers: []metrics.Server{}}
	if d.startedAt.IsZero() {
		snapshot.Uptime = 0
	}
	snapshot.Host, d.hostCounter, _ = metrics.ReadHost(d.hostCounter)
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	states := map[string]State{}
	for _, s := range d.servers {
		servers = append(servers, s)
		states[s.UUID] = d.states[s.UUID]
	}
	d.mu.Unlock()
	next := map[string]metrics.DockerStats{}
	for _, s := range servers {
		st := states[s.UUID]
		connected := st.Boot == s.Boot && time.Since(st.UpdatedAt) < 45*time.Second && st.Ready
		v := metrics.Server{ID: s.UUID, Name: s.Env["SERVER_HOSTNAME"], Game: s.Identity.Game, Running: s.Running, Connected: connected, Players: -1}
		if connected {
			v.Players = st.Players
			v.Map = st.Map
			if st.Name != "" {
				v.Name = st.Name
			}
		}
		if s.Running {
			sampleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			raw, e := d.Docker.Stats(sampleCtx, s.ID)
			cancel()
			if e != nil {
				v.Error = e.Error()
			} else {
				var old *metrics.DockerStats
				if previous, ok := d.metricsPrevious[s.UUID+":"+s.Boot]; ok {
					old = &previous
				}
				v.Sample = metrics.Measure(raw, old, now.Sub(d.metricAt))
				next[s.UUID+":"+s.Boot] = raw
			}
		}
		snapshot.Servers = append(snapshot.Servers, v)
	}
	d.metricsPrevious = next
	d.metricAt = now
	d.metricSnapshot = snapshot
	return snapshot
}
func (d *Daemon) metricsSnapshot() metrics.Snapshot {
	d.metricsMu.Lock()
	defer d.metricsMu.Unlock()
	return d.metricSnapshot
}
func (d *Daemon) runMetrics(ctx context.Context) {
	if !d.Config.Metrics.Enabled {
		return
	}
	listener, e := net.Listen("tcp", d.Config.Metrics.Listen)
	if e != nil {
		log.Printf("metrics listener: %v", e)
		return
	}
	server := &http.Server{Handler: metrics.Handler(d.metricsSnapshot), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { <-ctx.Done(); server.Close() }()
	go func() {
		if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
			log.Printf("metrics: %v", e)
		}
	}()
	ticker := time.NewTicker(time.Duration(d.Config.Metrics.SampleSeconds) * time.Second)
	defer ticker.Stop()
	for {
		d.sampleMetrics(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
