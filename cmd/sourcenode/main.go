package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/nodeinstall"
)

var version = "dev"

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "sourcenode:", e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "/etc/sourcenode/config.json", "node configuration")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return fmt.Errorf("usage: sourcenode [-config path] install [--yes] [--channel stable|beta]|init|serve|status|doctor|passwords show UUID|update GAME [BRANCH]|history GAME|pin UUID GENERATION|rollback GAME GENERATION|prune|version")
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if args[0] == "init" {
		if _, e := os.Stat(*path); e == nil {
			return fmt.Errorf("config already exists")
		}
		if e := os.MkdirAll(filepath.Dir(*path), 0700); e != nil {
			return e
		}
		root, e := os.OpenRoot(filepath.Dir(*path))
		if e != nil {
			return e
		}
		defer root.Close()
		return fsx.JSON(root, filepath.Base(*path), node.DefaultConfig(), 0600)
	}
	if args[0] == "install" {
		if os.Geteuid() != 0 {
			return fmt.Errorf("node installation requires root; run sudo sourcenode install")
		}
		options := flag.NewFlagSet("install", flag.ContinueOnError)
		yes := options.Bool("yes", false, "keep existing settings or install defaults without prompts")
		channel := options.String("channel", "", "stable or beta release channel")
		if err := options.Parse(args[1:]); err != nil {
			return err
		}
		if len(options.Args()) > 0 {
			return fmt.Errorf("unexpected install arguments")
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return nodeinstall.Install(ctx, nodeinstall.Options{ConfigPath: *path, Yes: *yes, Channel: *channel, Input: os.Stdin, Output: os.Stdout})
	}
	if args[0] == "uninstall" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return uninstallCLI(ctx, *path, args[1:])
	}
	config, e := node.LoadConfig(*path)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if handled, err := extendedCLI(ctx, config, args); handled {
		return err
	}
	switch args[0] {
	case "serve":
		d, e := node.New(config)
		if e != nil {
			return e
		}
		d.Version = version
		return d.Run(ctx)
	case "doctor":
		if data, err := node.Control(ctx, config, "GET", "/doctor"); err == nil {
			printJSON(data)
			return nil
		}
		if _, e := os.Stat(config.SteamCMD); e != nil {
			return e
		}
		if _, e := node.NewDocker().Servers(ctx, config.Images); e != nil {
			return e
		}
		_, e := node.LoadWings(config.WingsConfig)
		if e != nil {
			return e
		}
		fmt.Println("Docker, SteamCMD path, and Wings configuration available. Use live-client acceptance before production.")
		return nil
	case "status":
		data, e := node.Control(ctx, config, "GET", "/status")
		if e == nil {
			var v any
			if json.Unmarshal(data, &v) == nil {
				data, _ = json.MarshalIndent(v, "", "  ")
			}
			fmt.Println(string(data))
		}
		return e
	case "history":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: sourcenode history GAME [BRANCH]")
		}
		branch := "public"
		if len(args) == 3 {
			branch = args[2]
		}
		data, e := node.Control(ctx, config, "GET", "/history?game="+url.QueryEscape(args[1])+"&branch="+url.QueryEscape(branch))
		if e == nil {
			fmt.Println(string(data))
		}
		return e
	case "pin", "unpin":
		if (args[0] == "pin" && len(args) != 3) || (args[0] == "unpin" && len(args) != 2) {
			return fmt.Errorf("usage: sourcenode pin UUID GENERATION | unpin UUID")
		}
		generation := ""
		if args[0] == "pin" {
			generation = args[2]
		}
		data, e := node.Control(ctx, config, "POST", "/pin?server="+url.QueryEscape(args[1])+"&generation="+url.QueryEscape(generation))
		if e == nil {
			fmt.Print(string(data))
		}
		return e
	case "rollback", "unpin-game":
		if (args[0] == "rollback" && (len(args) < 3 || len(args) > 4)) || (args[0] == "unpin-game" && (len(args) < 2 || len(args) > 3)) {
			return fmt.Errorf("usage: sourcenode rollback GAME GENERATION [BRANCH] | unpin-game GAME [BRANCH]")
		}
		generation := ""
		branch := "public"
		if args[0] == "rollback" {
			generation = args[2]
			if len(args) == 4 {
				branch = args[3]
			}
		} else if len(args) == 3 {
			branch = args[2]
		}
		data, e := node.Control(ctx, config, "POST", "/rollback?game="+url.QueryEscape(args[1])+"&branch="+url.QueryEscape(branch)+"&generation="+url.QueryEscape(generation))
		if e == nil {
			fmt.Print(string(data))
		}
		return e
	case "passwords":
		if len(args) != 3 || args[1] != "show" {
			return fmt.Errorf("usage: sourcenode passwords show UUID")
		}
		data, e := node.Control(ctx, config, "GET", "/password?server="+url.QueryEscape(args[2]))
		if e == nil {
			fmt.Println(string(data))
		}
		return e
	case "update":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: sourcenode update GAME [BRANCH]")
		}
		branch := "public"
		if len(args) == 3 {
			branch = args[2]
		}
		data, e := node.Control(ctx, config, "POST", "/update?game="+url.QueryEscape(args[1])+"&branch="+url.QueryEscape(branch))
		if e == nil {
			fmt.Println(string(data))
		}
		return e
	case "prune":
		data, e := node.Control(ctx, config, "POST", "/prune")
		if e == nil {
			fmt.Print(string(data))
		}
		return e
	}
	return fmt.Errorf("unknown command %q", args[0])
}
