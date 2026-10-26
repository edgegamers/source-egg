package egg

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/supervisor"
)

// Run forwards the panel console through a PTY and stops srcds gracefully.
func (e *Egg) Run(ctx context.Context) error {
	c := e.Config
	if err := os.MkdirAll(filepath.Join(c.Dir, ".source/logs"), 0700); err != nil {
		return err
	}
	logfile, err := OpenLog(filepath.Join(c.Dir, ".source/logs"), 32<<20, 3)
	if err != nil {
		return err
	}
	defer logfile.Close()
	var output sync.Mutex
	var tail []string
	var delivery *consoleDelivery
	secrets := []string{c.Password, c.RCON, c.GSLT, c.SteamPassword}
	line := func(s string) {
		output.Lock()
		defer output.Unlock()
		masked := Mask(s, secrets)
		fmt.Fprintln(io.MultiWriter(os.Stdout, logfile), masked)
		if delivery != nil {
			delivery.offer(masked)
		}
		if len(masked) > 512 {
			masked = masked[:512]
		}
		tail = append(tail, masked)
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
	}
	previousProgress := e.Progress
	e.Progress = consoleWriter{line: line}
	defer func() { e.Progress = previousProgress }()
	prepared, err := e.Prepare(ctx)
	if err != nil {
		if ctx.Err() == nil && e.Node != nil && prepared.Identity.Validate() == nil {
			message := Mask(err.Error(), secrets)
			if len(message) > 512 {
				message = message[:512]
			}
			reportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = e.call(reportCtx, node.Request{Action: "boot-failed", Identity: prepared.Identity, Message: message})
			cancel()
		}
		return err
	}
	secrets = append(secrets, prepared.Password)
	if c.PersistLogs && e.Node != nil {
		delivery = newConsoleDelivery(e, prepared.Identity, line)
		for _, old := range tail {
			delivery.offer(old)
		}
	}
	cmd := exec.Command(prepared.Args[0], prepared.Args[1:]...)
	cmd.Dir = c.Dir
	server := &supervisor.Server{Cmd: cmd, Stdin: os.Stdin, Line: line, StopCommand: "quit", Grace: 30 * time.Second, Idle: 15 * time.Second}
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	if delivery != nil {
		workers.Go(func() { delivery.run(runctx) })
	}
	workers.Go(func() { e.watch(runctx, server, prepared, line) })
	line("SourceEgg | Starting " + prepared.Profile.Name)
	err = server.Run(runctx)
	cancel()
	workers.Wait()
	if err != nil && !server.Stopping() && e.Node != nil {
		reportCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		code := -1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		output.Lock()
		console := append([]string(nil), tail...)
		output.Unlock()
		_, _ = e.call(reportCtx, node.Request{Action: "crash", Identity: prepared.Identity, ExitCode: code, Console: console})
		done()
	}
	if server.Stopping() {
		return nil
	}
	return err
}
func (e *Egg) watch(ctx context.Context, server *supervisor.Server, p Prepared, line func(string)) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	revision := ""
	if p.Assignment != nil {
		revision = p.Assignment.Revision
	}
	checksSent := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			players := -1
			queryCtx, queryCancel := context.WithTimeout(ctx, 2*time.Second)
			info, err := QueryInfo(queryCtx, fmt.Sprintf("127.0.0.1:%d", e.Config.Port))
			queryCancel()
			ready := err == nil
			if ready {
				players = info.Players
				if !checksSent {
					_ = server.Send("meta version")
					if e.Config.InstallSourceMod {
						_ = server.Send("sm version")
					}
					checksSent = true
					line("SourceEgg | Ready")
				}
			}
			if e.Node == nil {
				continue
			}
			requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			res, err := e.call(requestCtx, node.Request{Action: "state", Identity: p.Identity, Players: players, Ready: ready, Generation: p.Generation, Map: info.Map, Name: info.Name})
			cancel()
			if err == nil && res.RestartSeconds > 0 {
				_ = server.Send(fmt.Sprintf("say Server update restart in approximately %d seconds", res.RestartSeconds))
			}
			if !e.Config.GeneratePassword {
				continue
			}
			requestCtx, cancel = context.WithTimeout(ctx, 20*time.Second)
			res, err = e.call(requestCtx, node.Request{Action: "password", Identity: p.Identity})
			cancel()
			if err != nil || res.Password == nil || res.Password.Revision == revision {
				continue
			}
			// Cache before requesting a restart. Do not print the assigned credential.
			root, err := os.OpenRoot(e.Config.Dir)
			if err != nil {
				continue
			}
			err = fsx.JSON(root, ".source/password.json", res.Password, 0600)
			root.Close()
			if err != nil {
				continue
			}
			requestCtx, cancel = context.WithTimeout(ctx, 20*time.Second)
			_, err = e.call(requestCtx, node.Request{Action: "password-restart", Identity: p.Identity, Revision: res.Password.Revision})
			cancel()
			if err == nil {
				revision = res.Password.Revision
			}
		}
	}
}
