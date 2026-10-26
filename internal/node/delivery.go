package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/edgegamers/source-egg/internal/content"
)

func (c Config) validateDelivery() error {
	if err := content.ValidateMethod(c.PushMethod); err != nil {
		return err
	}
	if c.MaxWorkers < 1 || c.MaxWorkers > 256 {
		return errors.New("max_workers must be between 1 and 256")
	}
	return nil
}

func (d *Daemon) acquirePreparation(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.prepareWorkers == nil {
		d.prepareWorkers = make(chan struct{}, d.Config.MaxWorkers)
	}
	workers := d.prepareWorkers
	d.mu.Unlock()
	select {
	case workers <- struct{}{}:
		return func() { <-workers }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type preparationKey struct{ uuid, boot string }
type preparationCall struct {
	done chan struct{}
	host bool
	out  Response
	err  error
}

// Coalesce retries before acquiring a worker. A waiter never owns another
// worker's slot, and cancelled waiters cannot strand preparation or pruning.
func (d *Daemon) prepareServer(ctx context.Context, s Server, host bool) (out Response, err error) {
	if err := ctx.Err(); err != nil {
		return out, err
	}
	key := preparationKey{s.UUID, s.Boot}
	d.mu.Lock()
	if d.prepareJobs == nil {
		d.prepareJobs = map[preparationKey]*preparationCall{}
	}
	if call := d.prepareJobs[key]; call != nil {
		d.mu.Unlock()
		if call.host != host {
			return out, errors.New("prepare capabilities changed during the same boot")
		}
		select {
		case <-call.done:
			return call.out, call.err
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
	call := &preparationCall{done: make(chan struct{}), host: host}
	d.prepareJobs[key] = call
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		call.out, call.err = out, err
		delete(d.prepareJobs, key)
		close(call.done)
		d.mu.Unlock()
	}()
	release, err := d.acquirePreparation(ctx)
	if err != nil {
		return out, err
	}
	defer release()
	d.prepare.RLock()
	defer d.prepare.RUnlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return d.prepareResponse(ctx, s, host)
}

func (d *Daemon) prepareResponse(ctx context.Context, s Server, host bool) (Response, error) {
	out := Response{PasswordDisabled: !d.Config.PasswordEnabled, Managed: d.Config.Shared && s.Env["SOURCE_MODE"] != "standalone", CleanupManaged: d.Config.Operations.Cleanup.Enabled}
	if out.Managed {
		if !host && d.Config.PushMethod != "symlink" {
			return out, errors.New("selected push_method requires an egg image with host delivery support")
		}
		g, e := d.desired(s)
		if os.IsNotExist(e) {
			g, e = d.Store.Ensure(ctx, s.Identity)
		}
		if e != nil {
			return out, e
		}
		out.Generation = g
		d.mu.Lock()
		ref := d.refs[s.UUID]
		ref.Pending = g.Key
		d.refs[s.UUID] = ref
		e = d.saveRefs()
		d.mu.Unlock()
		if e != nil {
			return out, e
		}
		if host {
			options := content.DeployOptions{Method: d.Config.PushMethod, PreserveOwnership: true}
			if options.Method == "hardlink" {
				uid, err := containerFileUID(s.PID)
				if err != nil {
					return out, fmt.Errorf("hardlink container ownership: %w", err)
				}
				options.ServerUID = &uid
			}
			if e = content.DeployWithOptions(g, s.Volume, SharedMount, options); e != nil {
				return out, e
			}
			out.Delivered = true
		}
	}
	if d.Config.FrameworkCache {
		for _, name := range []string{"metamod", "sourcemod"} {
			if !frameworkEnabled(s.Env, name) {
				continue
			}
			pkg, e := d.Cache.Resolve(ctx, name, d.frameworkPin(s, name))
			if e != nil {
				return out, e
			}
			archive, e := d.Cache.Fetch(ctx, pkg)
			if e != nil {
				return out, e
			}
			rel, e := filepath.Rel(d.Config.CacheDir, archive)
			if e != nil || !filepath.IsLocal(rel) {
				return out, errors.New("invalid cache receipt")
			}
			out.Frameworks = append(out.Frameworks, Framework{pkg, rel})
		}
	}
	if d.Config.PasswordEnabled && s.Env["GENERATE_PASSWORD"] == "1" {
		a, e := d.password(ctx, s)
		if e != nil {
			return out, e
		}
		out.Password = &a
	}
	return out, nil
}

func containerFileUID(pid int) (int, error) {
	if pid <= 0 {
		return 0, errors.New("container is not running")
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, err
	}
	return containerUIDFromStatus(string(data))
}

func containerUIDFromStatus(data string) (int, error) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[0] != "Uid:" {
			continue
		}
		// Refuse root or switchable identities: saved privileges could permit
		// changing shared inode permissions after the preparation check.
		for _, value := range fields[1:] {
			uid, e := strconv.Atoi(value)
			if e != nil || uid <= 0 || value != fields[4] {
				return 0, errors.New("hardlinks require a stable non-root container process UID")
			}
		}
		return strconv.Atoi(fields[4]) // Linux filesystem UID controls file writes.
	}
	return 0, errors.New("container process UID is unavailable")
}
