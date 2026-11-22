package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
)

func deliveryDaemon(t *testing.T) *Daemon {
	t.Helper()
	cfg := DefaultConfig()
	cfg.StateDir, cfg.SharedDir, cfg.CacheDir = t.TempDir(), t.TempDir(), t.TempDir()
	cfg.FrameworkCache = false
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPrepareDeliversSelectedCopyBeforeAcknowledging(t *testing.T) {
	d := deliveryDaemon(t)
	d.Config.PushMethod = "copy"
	d.Config.Operations.Cleanup.Enabled = true
	g := published(t, d.Config.SharedDir, "200")
	if err := os.WriteFile(filepath.Join(g.Path, "cstrike/pak01_dir.vpk"), []byte("stock"), 0644); err != nil {
		t.Fatal(err)
	}
	s := Server{UUID: "server", Boot: "boot", Volume: t.TempDir(), Identity: g.Identity}
	out, err := d.handle(context.Background(), s, Request{Action: "prepare", HostDelivery: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Managed || !out.Delivered || !out.CleanupManaged {
		t.Fatalf("incomplete response: %+v", out)
	}
	info, err := os.Lstat(filepath.Join(s.Volume, "cstrike/pak01_dir.vpk"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("copy not delivered: %v, %v", info, err)
	}
	if d.refs[s.UUID].Pending != g.Key {
		t.Fatal("generation was not reserved before deployment")
	}
}

func TestAlternateDeliveryRejectsOldEggWithoutCapability(t *testing.T) {
	d := deliveryDaemon(t)
	d.Config.PushMethod = "copy"
	g := published(t, d.Config.SharedDir, "200")
	s := Server{UUID: "server", Volume: t.TempDir(), Identity: g.Identity}
	if _, err := d.handle(context.Background(), s, Request{Action: "prepare"}); err == nil {
		t.Fatal("old egg would silently replace copy delivery with symlinks")
	}
}

func TestHardlinkPreparationRequiresLiveContainerOwnership(t *testing.T) {
	d := deliveryDaemon(t)
	d.Config.PushMethod = "hardlink"
	g := published(t, d.Config.SharedDir, "200")
	if err := os.WriteFile(filepath.Join(g.Path, "cstrike/pak01_dir.vpk"), []byte("stock"), 0644); err != nil {
		t.Fatal(err)
	}
	s := Server{UUID: "server", Boot: "boot", PID: 999999999, Volume: t.TempDir(), Identity: g.Identity}
	_, err := d.handle(context.Background(), s, Request{Action: "prepare", HostDelivery: true})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unverified container ownership was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Volume, ".source")); !os.IsNotExist(err) {
		t.Fatal("unverified hardlink preparation wrote to volume")
	}
}

func TestHardlinkOwnershipRejectsRootAndSwitchableProcessUIDs(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		want         int
	}{
		{"ordinary", "Name:\tsourceegg\nUid:\t1000\t1000\t1000\t1000\n", 1000},
		{"root", "Uid:\t0\t0\t0\t0\n", 0},
		{"saved root", "Uid:\t1000\t1000\t0\t1000\n", 0},
		{"switchable identity", "Uid:\t1001\t1001\t1000\t1001\n", 0},
		{"missing UID", "Name:\tsourceegg\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid, err := containerUIDFromStatus(tc.status)
			if tc.want == 0 {
				if err == nil {
					t.Fatalf("unsafe process can access shared inode as UID %d", uid)
				}
				return
			}
			if err != nil || uid != tc.want {
				t.Fatalf("uid=%d err=%v", uid, err)
			}
		})
	}
}

func TestNodeSteamValidationReachesSteamCMDArguments(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir, cfg.SharedDir, cfg.CacheDir = t.TempDir(), t.TempDir(), t.TempDir()
	cfg.SteamValidate = true
	cfg.SteamCMD = filepath.Join(t.TempDir(), "steamcmd")
	if err := os.WriteFile(cfg.SteamCMD, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$2/args\"\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	install := t.TempDir()
	if err := d.Store.Installer.Install(context.Background(), id, install); err == nil {
		t.Fatal("fixture command unexpectedly succeeded")
	}
	args, err := os.ReadFile(filepath.Join(install, "args"))
	if err != nil || !strings.Contains(string(args), "+app_update\n232330\nvalidate\n+quit\n") {
		t.Fatalf("SteamCMD lost validation: %q, %v", args, err)
	}
}

func TestDisabledAutoRestartSuppressesPersistedRolloutAndWarnings(t *testing.T) {
	d := deliveryDaemon(t)
	d.Config.AutoRestart = false
	d.Config.Restart = RestartPolicy{Mode: "immediate", WarnSeconds: 60}
	g := published(t, d.Config.SharedDir, "200")
	s := Server{UUID: "server", Identity: g.Identity, Running: true, Boot: "boot"}
	d.servers[s.UUID] = s
	d.states[s.UUID] = State{Boot: s.Boot, Players: 0, UpdatedAt: time.Now()}
	d.refs[s.UUID] = Reference{Current: "generations/css/public/linux-auto/100"}
	d.pending[s.UUID] = pending{Generation: g.Key, Since: time.Now().Add(-time.Hour), WarnedAt: time.Now()}
	// A nil Docker client ensures a suppressed rollout never attempts a restart.
	d.Docker = nil
	d.rollout(context.Background())
	out, err := d.handle(context.Background(), s, Request{Action: "state", Players: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.RestartSeconds != 0 {
		t.Fatal("disabled update restarts still warned players")
	}
	if !d.pending[s.UUID].RestartedAt.IsZero() {
		t.Fatal("disabled rollout marked a restart")
	}
	if _, err := d.handle(context.Background(), s, Request{Action: "prepare"}); err != nil {
		t.Fatal("disabled restarts prevented manual boot preparation", err)
	}
}

func TestUnknownPlayersWaitForWindowOrDeadline(t *testing.T) {
	noon := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	p := RestartPolicy{Mode: "window", Window: "03:00-04:00", MaxDelayMinutes: 60}
	if Due(p, noon, noon, -1) {
		t.Fatal("unknown players treated as empty")
	}
	if !Due(p, noon.Add(-time.Hour), noon, -1) {
		t.Fatal("unknown players ignored configured deadline")
	}
}

// A blocked Steam installer makes the worker admission limit observable
// without timing disk I/O or replacing the node's own synchronization.
type deliveryBlockingInstaller struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (b *deliveryBlockingInstaller) Install(ctx context.Context, id game.Identity, dir string) error {
	b.calls.Add(1)
	b.started <- struct{}{}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
	}
	return errors.New("fixture installer stopped")
}

func TestPreparationCoalescesOneServerAndCancelledWaiterReleasesSlot(t *testing.T) {
	d := deliveryDaemon(t)
	d.Config.MaxWorkers = 1
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	installer := &deliveryBlockingInstaller{started: make(chan struct{}, 10), release: make(chan struct{})}
	d.Store.Installer = installer
	s := Server{UUID: "same", Boot: "boot", Identity: id}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = d.handle(ctx, s, Request{Action: "prepare"}) }()
	}
	select {
	case <-installer.started:
	case <-ctx.Done():
		t.Fatal("initial preparation never ran")
	}
	waiter, cancelWaiter := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancelWaiter()
	_, err := d.handle(waiter, Server{UUID: "cancelled", Boot: "boot", Identity: id}, Request{Action: "prepare"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled admission = %v", err)
	}
	// Wait until all callers have reached the coalesced in-flight operation.
	time.Sleep(30 * time.Millisecond)
	close(installer.release)
	wg.Wait()
	if calls := installer.calls.Load(); calls != 1 {
		t.Fatalf("same server prepared %d times", calls)
	}
	if ctx.Err() != nil {
		t.Fatal("preparation deadlocked", ctx.Err())
	}
	_, _ = d.handle(ctx, Server{UUID: "next", Boot: "boot", Identity: id}, Request{Action: "prepare"})
	if installer.calls.Load() != 2 {
		t.Fatal("cancelled waiter leaked worker slot")
	}
}

func TestPreparationWorkerLimitAllowsIndependentServers(t *testing.T) {
	d := deliveryDaemon(t)
	d.Config.MaxWorkers = 2
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Admit independent work directly through the same gate used by preparation.
	release, err := d.acquirePreparation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	release2, err := d.acquirePreparation(ctx)
	if err != nil {
		t.Fatal("independent worker blocked", err)
	}
	defer release2()
	full, cancelFull := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancelFull()
	if _, err := d.acquirePreparation(full); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(fmt.Sprintf("worker bound ignored: %v", err))
	}
}
