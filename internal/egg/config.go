package egg

import (
	"errors"
	"strconv"
	"strings"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/game"
)

type Config struct {
	Dir, Game, Branch, Mode, Map, Hostname, Password, RCON, GSLT, SteamCMD, SteamUsername, SteamPassword, MetamodVersion, SourceModVersion string
	Port, MaxPlayers, NodeWaitSeconds                                                                                                      int
	InstallMetamod, InstallSourceMod, GeneratePassword, UpdateGame, Validate                                                               bool
	PersistLogs                                                                                                                            bool
	CleanupEnabled, EnableFilter                                                                                                           bool
	CustomParams, ConsoleLogLevel                                                                                                          string
	GDBDebugPort                                                                                                                           int
	SourceModPlugins                                                                                                                       map[string]string
}

func FromEnv(get func(string) string) (Config, error) {
	p, e := game.Lookup(get("SOURCE_GAME"))
	if e != nil {
		return Config{}, e
	}
	c := Config{Dir: "/home/container", Game: p.Key, Branch: get("STEAM_BRANCH"), Mode: get("SOURCE_MODE"), Map: get("SRCDS_MAP"), Hostname: get("SERVER_HOSTNAME"), Password: get("SERVER_PASSWORD"), RCON: get("RCON_PASSWORD"), GSLT: get("STEAM_GSLT"), SteamCMD: "/home/container/.source/steamcmd/steamcmd.sh", SteamUsername: get("STEAM_USERNAME"), SteamPassword: get("STEAM_PASSWORD"), MetamodVersion: get("METAMOD_VERSION"), SourceModVersion: get("SOURCEMOD_VERSION"), InstallMetamod: get("INSTALL_METAMOD") != "0", InstallSourceMod: get("INSTALL_SOURCEMOD") != "0", GeneratePassword: get("GENERATE_PASSWORD") == "1", UpdateGame: get("AUTO_UPDATE") != "0", Validate: get("STEAM_VALIDATE") == "1", NodeWaitSeconds: 20}
	if c.Mode == "" {
		c.Mode = "auto"
	}
	c.PersistLogs = get("ENABLE_LOG_FILES") != "0"
	c.CleanupEnabled = get("CLEANUP_ENABLED") == "1"
	c.EnableFilter = get("ENABLE_FILTER") == "1"
	c.CustomParams = get("CUSTOM_PARAMS")
	c.ConsoleLogLevel = get("CONSOLE_LOG_LEVEL")
	c.SourceModPlugins = map[string]string{}
	for _, name := range addons.StockPlugins() {
		c.SourceModPlugins[name] = get("SOURCEMOD_PLUGIN_" + strings.ToUpper(name))
	}
	if e = addons.ValidatePluginOptions(c.SourceModPlugins); e != nil {
		return c, e
	}
	if c.ConsoleLogLevel != "" {
		if _, e = consoleLevel(c.ConsoleLogLevel); e != nil {
			return c, e
		}
	}
	if value := get("NODE_WAIT_SECONDS"); value != "" {
		c.NodeWaitSeconds, e = strconv.Atoi(value)
		if e != nil || c.NodeWaitSeconds < 0 || c.NodeWaitSeconds > 300 {
			return c, errors.New("NODE_WAIT_SECONDS must be between 0 and 300")
		}
	}
	c.Port, e = strconv.Atoi(get("SERVER_PORT"))
	if e != nil {
		return c, errors.New("SERVER_PORT must be an integer")
	}
	if raw := get("GDB_DEBUG_PORT"); raw != "" {
		c.GDBDebugPort, e = strconv.Atoi(raw)
		if e != nil || c.GDBDebugPort < 1 || c.GDBDebugPort > 65535 || c.GDBDebugPort == c.Port {
			return c, errors.New("GDB_DEBUG_PORT must be 1–65535 and differ from SERVER_PORT")
		}
	}
	if s := get("MAX_PLAYERS"); s != "" {
		c.MaxPlayers, e = strconv.Atoi(s)
		if e != nil {
			return c, errors.New("MAX_PLAYERS must be an integer")
		}
	}
	if _, e = p.Identity(c.Branch); e != nil {
		return c, e
	}
	if c.Mode != "auto" && c.Mode != "managed" && c.Mode != "standalone" {
		return c, errors.New("SOURCE_MODE must be auto, managed, or standalone")
	}
	if _, e = p.Command(game.Launch{Port: c.Port, MaxPlayers: c.MaxPlayers, Map: c.Map, Password: c.Password, RCON: c.RCON, Hostname: c.Hostname, GSLT: c.GSLT, CustomParams: c.CustomParams}); e != nil {
		return c, e
	}
	return c, nil
}
