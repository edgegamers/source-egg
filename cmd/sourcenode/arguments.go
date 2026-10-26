package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type cliOptions struct {
	Config     string
	Args       []string
	JSON, Help bool
}

// Global flags are accepted on either side of a command. Command-specific
// flags stay in Args for their existing parsers.
func parseCLIArgs(args []string) (cliOptions, error) {
	options := cliOptions{Config: "/etc/sourcenode/config.json"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json" || arg == "-json":
			options.JSON = true
		case arg == "--help" || arg == "-h" || arg == "help" && len(args) == 1:
			options.Help = true
		case arg == "--config" || arg == "-config":
			i++
			if i >= len(args) || args[i] == "" {
				return options, errors.New("--config requires a path")
			}
			options.Config = args[i]
		case strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-config="):
			_, options.Config, _ = strings.Cut(arg, "=")
			if options.Config == "" {
				return options, errors.New("--config requires a path")
			}
		default:
			options.Args = append(options.Args, arg)
		}
	}
	return options, nil
}

func selectServerID(data []byte, selector string) (string, error) {
	if selector == "" {
		return "", errors.New("server identifier is required")
	}
	var response struct {
		Servers []struct {
			Server string `json:"server"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", err
	}
	var matches []string
	for _, server := range response.Servers {
		if server.Server == selector {
			return selector, nil
		}
		if strings.HasPrefix(server.Server, selector) {
			matches = append(matches, server.Server)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("server prefix must match exactly one server (matched %d); use status --json for full IDs", len(matches))
	}
	return matches[0], nil
}

const commandUsage = `Usage: sourcenode [--config PATH] [--json] COMMAND

Node
  install [--yes] [--channel stable|beta|dev]
  init | serve | status | doctor | modules | version
  self-update [--check] [--channel stable|beta|dev] [--release TAG]
  uninstall [--purge] [--yes]

Games and frameworks
  update GAME [BRANCH]             Prepare a new game generation
  history GAME [BRANCH]            List saved generations
  pin SERVER GENERATION | unpin SERVER
  rollback GAME GENERATION [BRANCH] | unpin-game GAME [BRANCH]
  pins | frameworks               Show fleet pins and installed versions
  pin FRAMEWORK VERSION [SERVER] | unpin FRAMEWORK [SERVER]
  prune                           Remove unreferenced game generations

Operations and diagnostics
  backups [SERVER] | backup now [SERVER]
  restore SERVER [INDEX] [--yes]   Restore to a stopped server; 1 is newest
  cleanup [now] [SERVER]
  maintenance [now --apply | setup --yes]
  logs [SERVER] | crashes [SERVER] | crash SERVER [INDEX]
  metrics | top
  passwords show SERVER           Explicitly reveal the generated join password

SERVER accepts a full UUID or an unambiguous prefix. FRAMEWORK is metamod,
sourcemod, or all. --json may appear before or after a command and retains
full IDs and the control API schema. top --json returns one metrics snapshot.
`
