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
	"github.com/edgegamers/source-egg/internal/terminalui"
)

var version = "dev"

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "sourcenode:", terminalui.Plain(e.Error()))
		os.Exit(1)
	}
}
func run() error {
	options, err := parseCLIArgs(os.Args[1:])
	if err != nil {
		return err
	}
	path := &options.Config
	args := options.Args
	if options.Help {
		fmt.Fprint(os.Stdout, commandUsage)
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%s", commandUsage)
	}

	if args[0] == "version" {
		if options.JSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"version": version})
		}
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
		channel := options.String("channel", "", "stable, beta or dev release channel")
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
	if handled, err := extendedCLI(ctx, config, args, options.JSON); handled {
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
			return renderResponse(os.Stdout, "doctor", data, options.JSON)
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
		message := "Docker, SteamCMD path, and Wings configuration available. Use live-client acceptance before production."
		if options.JSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": message})
		}
		fmt.Println(message)
		return nil
	case "status":
		data, e := node.Control(ctx, config, "GET", "/status")
		if e == nil {
			return renderResponse(os.Stdout, "status", data, options.JSON)
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
			return renderResponse(os.Stdout, "history", data, options.JSON)
		}
		return e
	case "pin", "unpin":
		if (args[0] == "pin" && len(args) != 3) || (args[0] == "unpin" && len(args) != 2) {
			return fmt.Errorf("usage: sourcenode pin UUID GENERATION | unpin UUID")
		}
		server, e := resolveServerID(ctx, config, args[1])
		if e != nil {
			return e
		}
		generation := ""
		if args[0] == "pin" {
			generation = args[2]
		}
		data, e := node.Control(ctx, config, "POST", "/pin?server="+url.QueryEscape(server)+"&generation="+url.QueryEscape(generation))
		if e == nil {
			return renderResponse(os.Stdout, "pin", data, options.JSON)
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
			return renderResponse(os.Stdout, "rollback", data, options.JSON)
		}
		return e
	case "passwords":
		if len(args) != 3 || args[1] != "show" {
			return fmt.Errorf("usage: sourcenode passwords show UUID")
		}
		server, e := resolveServerID(ctx, config, args[2])
		if e != nil {
			return e
		}
		data, e := node.Control(ctx, config, "GET", "/password?server="+url.QueryEscape(server))
		if e == nil {
			return renderResponse(os.Stdout, "password", data, options.JSON)
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
			return renderResponse(os.Stdout, "update", data, options.JSON)
		}
		return e
	case "prune":
		data, e := node.Control(ctx, config, "POST", "/prune")
		if e == nil {
			return renderResponse(os.Stdout, "prune", data, options.JSON)
		}
		return e
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func resolveServerID(ctx context.Context, config node.Config, selector string) (string, error) {
	data, err := node.Control(ctx, config, "GET", "/status")
	if err != nil {
		return "", err
	}
	return selectServerID(data, selector)
}
