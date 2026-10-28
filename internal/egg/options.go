package egg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/operations"
)

// Seed only missing files. Invalid user edits are reported without overwriting.
func serverConfig(root *os.Root, name string, dst any) error {
	for _, dir := range []string{".source", ".source/configs"} {
		if err := root.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		st, err := root.Lstat(dir)
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("server config directory must not be a symlink")
		}
	}
	name = ".source/configs/" + name
	st, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return fsx.JSON(root, name, dst, 0600)
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > 256<<10 {
		return errors.New("server config must be a regular file of at most 256 KiB")
	}
	var raw json.RawMessage
	if err = fsx.ReadJSON(root, name, &raw); err != nil {
		return err
	}
	if len(raw) > 256<<10 {
		return errors.New("server config exceeds 256 KiB")
	}
	if err = json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (e *Egg) cleanup(ctx context.Context, root *os.Root, nodeManaged bool) error {
	if !e.Config.CleanupEnabled || nodeManaged {
		return nil
	}
	cfg := operations.DefaultConfig()
	settings := struct {
		OnStart  bool              `json:"on_start"`
		KeepOpen bool              `json:"keep_open"`
		Rules    []operations.Rule `json:"rules"`
	}{cfg.Cleanup.OnStart, cfg.Cleanup.KeepOpen, cfg.Cleanup.Rules}
	if err := serverConfig(root, "cleanup.json", &settings); err != nil {
		return err
	}
	cfg.Cleanup.OnStart, cfg.Cleanup.KeepOpen, cfg.Cleanup.Rules = settings.OnStart, settings.KeepOpen, settings.Rules
	cfg.Cleanup.Enabled = true
	// Standalone cleanup is prelaunch only; the node owns scheduled cleanup.
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !cfg.Cleanup.OnStart {
		return nil
	}
	m, err := operations.New(cfg, filepath.Join(e.Config.Dir, ".source/cleanup"), operations.Options{})
	if err != nil {
		return err
	}
	r, err := m.Cleanup(ctx, operations.Server{ID: "standalone", Game: e.Config.Game, Volume: e.Config.Dir, PID: os.Getpid()})
	if err != nil {
		return err
	}
	e.progress(fmt.Sprintf("Cleanup removed %d files (%d bytes)", r.Files, r.Bytes))
	return nil
}

type consoleFilter struct {
	PreviewMode bool     `json:"preview_mode"`
	Patterns    []string `json:"patterns"`
}
type consoleSettings struct {
	filter  consoleFilter
	enabled bool
	level   int
}

func consoleLevel(level string) (int, error) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 0, nil
	case "INFO", "":
		return 1, nil
	case "WARN", "WARNING":
		return 2, nil
	case "ERROR":
		return 3, nil
	default:
		return 0, errors.New("CONSOLE_LOG_LEVEL must be DEBUG, INFO, WARNING or ERROR")
	}
}
func (e *Egg) consoleSettings() (consoleSettings, error) {
	root, err := os.OpenRoot(e.Config.Dir)
	if err != nil {
		return consoleSettings{}, err
	}
	defer root.Close()
	var logging struct {
		Logging struct {
			ConsoleLevel string `json:"console_level"`
		} `json:"logging"`
	}
	logging.Logging.ConsoleLevel = "INFO"
	if err = serverConfig(root, "logging.json", &logging); err != nil {
		return consoleSettings{}, err
	}
	if e.Config.ConsoleLogLevel != "" {
		logging.Logging.ConsoleLevel = e.Config.ConsoleLogLevel
	}
	level, err := consoleLevel(logging.Logging.ConsoleLevel)
	if err != nil {
		return consoleSettings{}, err
	}
	s := consoleSettings{level: level, enabled: e.Config.EnableFilter, filter: consoleFilter{Patterns: []string{}}}
	if err = serverConfig(root, "console-filter.json", &s.filter); err != nil {
		return s, err
	}
	if len(s.filter.Patterns) > 256 {
		return s, errors.New("console filter exceeds 256 patterns")
	}
	for _, p := range s.filter.Patterns {
		if p == "" || p == "@" || len(p) > 1024 || strings.ContainsAny(p, "\r\n\x00") {
			return s, errors.New("invalid console filter pattern")
		}
	}
	return s, nil
}

// Only console presentation is filtered; callers persist the masked original.
func (s consoleSettings) display(masked string) (string, bool) {
	// Preserve the Wings lifecycle handshake and owner-action plugin notices
	// through thresholds, preview mode and custom filters. Both are masked first.
	if masked == panelReadyLine || strings.HasPrefix(masked, pluginNoticePrefix) {
		return masked, true
	}
	level := 1
	for _, prefix := range []struct {
		text  string
		level int
	}{{"DEBUG", 0}, {"INFO", 1}, {"WARN", 2}, {"WARNING", 2}, {"ERROR", 3}} {
		if strings.HasPrefix(masked, "["+prefix.text+"]") || strings.HasPrefix(masked, "SourceEgg | "+prefix.text+" |") {
			level = prefix.level
			break
		}
	}
	if s.enabled && strings.TrimSpace(masked) != "" {
		for _, p := range s.filter.Patterns {
			exact := strings.HasPrefix(p, "@")
			if (exact && masked == p[1:]) || (!exact && strings.Contains(masked, p)) {
				if !s.filter.PreviewMode {
					return "", false
				}
				return "SourceEgg | DEBUG | Filter preview: " + masked, s.level == 0
			}
		}
	}
	return masked, level >= s.level || masked == ""
}
