package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/lifecycle"
	"github.com/edgegamers/source-egg/internal/metrics"
	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/nodeinstall"
	"github.com/edgegamers/source-egg/internal/terminalui"
)

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}
func withoutFlags(args []string, names ...string) []string {
	var out []string
	for _, a := range args {
		skip := false
		for _, name := range names {
			if a == name {
				skip = true
			}
		}
		if !skip {
			out = append(out, a)
		}
	}
	return out
}
func confirm(input io.Reader, output io.Writer, action string, yes bool) error {
	if yes {
		return nil
	}
	fmt.Fprintf(output, "%s? [y/N]: ", terminalui.Plain(action))
	s, e := bufio.NewReader(input).ReadString('\n')
	if e != nil && s == "" {
		return errors.New("confirmation required (or pass --yes)")
	}
	if strings.EqualFold(strings.TrimSpace(s), "y") || strings.EqualFold(strings.TrimSpace(s), "yes") {
		return nil
	}
	return errors.New("cancelled")
}
func operatorRoute(args []string) (method, path, prompt string, handled bool, err error) {
	if len(args) == 0 {
		return
	}
	a := withoutFlags(args, "--yes", "--json")
	usage := func(s string) (string, string, string, bool, error) {
		return "", "", "", true, fmt.Errorf("usage: sourcenode %s", s)
	}
	query := func(base string) string {
		if len(a) == 2 {
			return base + "?server=" + url.QueryEscape(a[1])
		}
		return base
	}
	switch a[0] {
	case "backups", "logs", "crashes":
		if len(a) > 2 {
			return usage(a[0] + " [SERVER]")
		}
		return "GET", query("/" + a[0]), "", true, nil
	case "backup":
		if len(a) < 2 || a[1] != "now" || len(a) > 3 {
			return usage("backup now [SERVER]")
		}
		path = "/backup"
		if len(a) == 3 {
			path += "?server=" + url.QueryEscape(a[2])
		}
		return "POST", path, "", true, nil
	case "restore", "crash":
		if len(a) < 2 || len(a) > 3 {
			return usage(a[0] + " SERVER [INDEX] [--yes]")
		}
		index := 1
		if len(a) == 3 {
			index, err = strconv.Atoi(a[2])
			if err != nil || index < 1 || index > 10000 {
				return usage(a[0] + " SERVER [INDEX 1–10000]")
			}
		}
		path = "/" + a[0] + "?server=" + url.QueryEscape(a[1]) + "&index=" + strconv.Itoa(index)
		if a[0] == "restore" {
			return "POST", path, "Restore backup over files on stopped server " + a[1] + " (unrelated files remain)", true, nil
		}
		return "GET", path, "", true, nil
	case "cleanup":
		if len(a) >= 2 && a[1] == "now" {
			if len(a) > 3 {
				return usage("cleanup now [SERVER]")
			}
			path = "/cleanup"
			if len(a) == 3 {
				path += "?server=" + url.QueryEscape(a[2])
			}
			return "POST", path, "", true, nil
		}
		if len(a) > 2 {
			return usage("cleanup [now] [SERVER]")
		}
		return "GET", query("/cleanup"), "", true, nil
	case "maintenance":
		if len(a) == 1 {
			return "GET", "/maintenance", "", true, nil
		}
		if a[1] == "now" {
			a = withoutFlags(a, "--apply")
			if len(a) != 2 {
				return usage("maintenance now [--apply]")
			}
			return "POST", "/maintenance?apply=" + strconv.FormatBool(hasFlag(args, "--apply")), "", true, nil
		}
		if a[1] == "setup" && len(a) == 2 {
			return "POST", "/maintenance/setup", "Enable unattended security upgrades (automatic reboot stays disabled)", true, nil
		}
		return usage("maintenance [now --apply | setup --yes]")
	case "pins", "frameworks":
		if len(a) != 1 {
			return usage("pins")
		}
		return "GET", "/pins", "", true, nil
	case "pin", "unpin":
		if len(a) < 2 || (a[1] != "metamod" && a[1] != "sourcemod" && a[1] != "all") {
			return "", "", "", false, nil
		}
		pin := "latest"
		server := ""
		if a[0] == "pin" {
			if len(a) < 3 || len(a) > 4 {
				return usage("pin FRAMEWORK VERSION [SERVER]")
			}
			pin = a[2]
			if len(a) == 4 {
				server = a[3]
			}
		} else {
			if len(a) > 3 {
				return usage("unpin FRAMEWORK [SERVER]")
			}
			if len(a) == 3 {
				server = a[2]
			}
		}
		path = "/framework-pin?name=" + url.QueryEscape(a[1]) + "&version=" + url.QueryEscape(pin)
		if server != "" {
			path += "&server=" + url.QueryEscape(server)
		}
		return "POST", path, "", true, nil
	case "metrics", "modules":
		if len(a) != 1 {
			return usage(a[0])
		}
		return "GET", "/" + a[0], "", true, nil
	}
	return "", "", "", false, nil
}
func extendedCLI(ctx context.Context, c node.Config, args []string, jsonOutput bool) (bool, error) {
	if args[0] == "top" {
		if jsonOutput {
			data, err := node.Control(ctx, c, "GET", "/metrics")
			if err == nil {
				err = renderResponse(os.Stdout, "metrics", data, true)
			}
			return true, err
		}
		return true, metrics.Top(ctx, os.Stdin, os.Stdout, func(ctx context.Context) (metrics.Snapshot, error) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			var snapshot metrics.Snapshot
			b, e := node.Control(ctx, c, "GET", "/metrics")
			if e == nil {
				e = json.Unmarshal(b, &snapshot)
			}
			return snapshot, e
		})
	}
	if args[0] == "self-update" || args[0] == "update" && (len(args) == 1 || strings.HasPrefix(args[1], "--")) {
		return true, selfUpdateCLI(ctx, c, args[1:], jsonOutput)
	}
	method, path, prompt, handled, e := operatorRoute(args)
	if !handled || e != nil {
		return handled, e
	}
	if prompt != "" {
		promptOutput := os.Stdout
		if jsonOutput {
			promptOutput = os.Stderr
		}
		if e = confirm(os.Stdin, promptOutput, prompt, hasFlag(args, "--yes")); e != nil {
			return true, e
		}
	}
	b, e := node.Control(ctx, c, method, path)
	if e == nil {
		e = renderResponse(os.Stdout, path, b, jsonOutput)
	}
	return true, e
}
func selfUpdateCLI(ctx context.Context, c node.Config, args []string, jsonOutput bool) error {
	if os.Geteuid() != 0 {
		return errors.New("node update requires root")
	}
	f := flag.NewFlagSet("self-update", flag.ContinueOnError)
	check := f.Bool("check", false, "check without replacing binary")
	channel := f.String("channel", "", "stable, beta or dev")
	tag := f.String("release", "", "exact release tag")
	f.Bool("yes", false, "accepted for unattended commands")
	if e := f.Parse(args); e != nil {
		return e
	}
	if len(f.Args()) > 0 {
		return errors.New("usage: sourcenode self-update [--check] [--channel stable|beta|dev] [--release TAG]")
	}
	cfg := c.SelfUpdate
	if cfg.Channel == "" {
		cfg.Channel = c.NodeChannel
	}
	if *channel != "" {
		cfg.Channel = *channel
	}
	if *tag != "" {
		cfg.Release = *tag
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	m, e := lifecycle.New(cfg, lifecycle.Options{BinaryPath: exe, BeforeRestart: func(ctx context.Context, version string) error {
		r, e := os.OpenRoot(c.StateDir)
		if e != nil {
			return e
		}
		defer r.Close()
		return fsx.JSON(r, "pending-node-update.json", map[string]string{"version": version}, 0600)
	}})
	if e != nil {
		return e
	}
	candidate, e := m.Check(ctx, version)
	if errors.Is(e, lifecycle.ErrUpToDate) {
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "up to date"})
		}
		fmt.Println("Node is up to date")
		return nil
	}
	if e != nil {
		return e
	}
	if *check {
		data, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		return renderResponse(os.Stdout, "self-update", data, jsonOutput)
	}
	if e = m.Apply(ctx, candidate); e != nil {
		return e
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": candidate.Version, "installed": true, "restart_requested": true})
	}
	fmt.Println("Installed node", terminalui.Plain(candidate.Version), "and requested a service restart")
	return nil
}
func uninstallCLI(ctx context.Context, configPath string, args []string) error {
	return uninstallCLIWith(ctx, configPath, args, nodeinstall.Uninstall)
}
func uninstallCLIWith(ctx context.Context, configPath string, args []string, uninstall func(context.Context, nodeinstall.UninstallOptions) error) error {
	if os.Geteuid() != 0 {
		return errors.New("node removal requires root")
	}
	f := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := f.Bool("purge", false, "also remove the config file; keep all game/state data")
	yes := f.Bool("yes", false, "skip interactive confirmation")
	if e := f.Parse(args); e != nil {
		return e
	}
	if len(f.Args()) > 0 {
		return errors.New("usage: sourcenode uninstall [--purge] [--yes]")
	}
	if e := confirm(os.Stdin, os.Stdout, "Remove the node service/binary while preserving shared files, server volumes and private state", *yes); e != nil {
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	if e := uninstall(ctx, nodeinstall.UninstallOptions{BinaryPath: exe, UnitPath: "/etc/systemd/system/sourcenode.service", ConfigPath: configPath, Purge: *purge}); e != nil {
		return e
	}
	fmt.Println("Node removed. Shared files, server volumes, backups and private state retained.")
	return nil
}
