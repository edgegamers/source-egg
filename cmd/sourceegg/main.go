package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/edgegamers/source-egg/internal/egg"
	"github.com/edgegamers/source-egg/internal/game"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(version)
			return
		case "profiles":
			json.NewEncoder(os.Stdout).Encode(game.All())
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: sourceegg [version|profiles]")
			os.Exit(2)
		}
	}
	config, e := egg.FromEnv(os.Getenv)
	if e != nil {
		fmt.Fprintln(os.Stderr, "SourceEgg:", e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runtime := egg.Egg{Config: config}
	if e = runtime.Run(ctx); e != nil {
		fmt.Fprintln(os.Stderr, "SourceEgg:", egg.Mask(e.Error(), []string{config.Password, config.RCON, config.GSLT, config.SteamPassword}))
		os.Exit(1)
	}
}
