package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
)

func (d *Daemon) control() error {
	path := filepath.Join(d.Config.StateDir, "control.sock")
	os.Remove(path)
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	if e = os.Chmod(path, 0600); e != nil {
		l.Close()
		return e
	}
	mux := http.NewServeMux()
	d.moduleControl(mux)
	mux.HandleFunc("POST /pin", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("generation")
		if err := d.pinServer(r.URL.Query().Get("server"), key); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		d.reconcile()
		io.WriteString(w, "server generation selection saved\n")
	})
	mux.HandleFunc("POST /rollback", func(w http.ResponseWriter, r *http.Request) {
		p, err := game.Lookup(r.URL.Query().Get("game"))
		if err != nil {
			http.Error(w, "unknown game", 400)
			return
		}
		id, err := p.Identity(r.URL.Query().Get("branch"))
		if err != nil {
			http.Error(w, "invalid branch", 400)
			return
		}
		if err = d.pinGame(id, r.URL.Query().Get("generation")); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		d.reconcile()
		io.WriteString(w, "game generation selection saved\n")
	})
	mux.HandleFunc("GET /history", func(w http.ResponseWriter, r *http.Request) {
		p, err := game.Lookup(r.URL.Query().Get("game"))
		if err != nil {
			http.Error(w, "unknown game", 400)
			return
		}
		id, err := p.Identity(r.URL.Query().Get("branch"))
		if err != nil {
			http.Error(w, "invalid branch", 400)
			return
		}
		history, err := d.Store.History(id)
		if err != nil {
			http.Error(w, "history unavailable", 404)
			return
		}
		json.NewEncoder(w).Encode(history)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		servers := d.snapshot()
		d.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"servers": servers, "modules": d.moduleStatus()})
	})
	mux.HandleFunc("GET /password", func(w http.ResponseWriter, r *http.Request) {
		uuid := r.URL.Query().Get("server")
		d.mu.Lock()
		s, ok := d.servers[uuid]
		d.mu.Unlock()
		if !ok {
			http.Error(w, "unknown server", 404)
			return
		}
		a, e := d.password(r.Context(), s)
		if e != nil {
			http.Error(w, "password unavailable", 503)
			return
		}
		json.NewEncoder(w).Encode(a)
	})
	mux.HandleFunc("POST /update", func(w http.ResponseWriter, r *http.Request) {
		p, e := game.Lookup(r.URL.Query().Get("game"))
		if e != nil {
			http.Error(w, "unknown game", 400)
			return
		}
		id, e := p.Identity(r.URL.Query().Get("branch"))
		if e != nil {
			http.Error(w, "invalid branch", 400)
			return
		}
		g, e := d.Store.Ensure(r.Context(), id)
		if e != nil {
			http.Error(w, "update failed; see journal", 503)
			return
		}
		d.mu.Lock()
		for _, s := range d.servers {
			if s.Running && s.Identity == id && s.Env["SOURCE_MODE"] != "standalone" && d.refs[s.UUID].Current != g.Key {
				d.pending[s.UUID] = pending{Generation: g.Key, Since: time.Now()}
			}
		}
		d.mu.Unlock()
		json.NewEncoder(w).Encode(g)
	})
	mux.HandleFunc("POST /prune", func(w http.ResponseWriter, r *http.Request) {
		d.prepare.Lock()
		defer d.prepare.Unlock()
		d.mu.Lock()
		references := map[string]bool{}
		for _, ref := range d.refs {
			references[ref.Current] = true
			references[ref.Pending] = true
		}
		for _, key := range d.pins {
			references[key] = true
		}
		d.mu.Unlock()
		if e := d.Store.Prune(references); e != nil {
			http.Error(w, "prune failed", 503)
			return
		}
		io.WriteString(w, "pruned unreferenced game generations\n")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	d.mu.Lock()
	d.listeners["control"] = l
	d.mu.Unlock()
	go server.Serve(l)
	return nil
}
func Control(ctx context.Context, c Config, method, path string) ([]byte, error) {
	client := SocketClient(filepath.Join(c.StateDir, "control.sock"))
	defer client.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, method, "http://sourcenode"+path, nil)
	if e != nil {
		return nil, e
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		return nil, errors.New(string(data))
	}
	return data, nil
}
