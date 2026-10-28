package egg

import (
	"context"
	"fmt"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/plugins"
)

func (e *Egg) recoverPlugins() error {
	p, err := game.Lookup(e.Config.Game)
	if err != nil {
		return err
	}
	installer := plugins.Installer{Volume: e.Config.Dir, GameDir: p.Dir}
	if err := installer.Recover(); err != nil {
		return fmt.Errorf("plugin transaction recovery: %w", err)
	}
	return nil
}

// Prepare the manager after frameworks establish their ownership receipts.
// Invalid editable configuration disables plugin work without preventing the
// game from starting; a later console command can retry after the owner fixes it.
func (e *Egg) preparePlugins(ctx context.Context, p Prepared, notice func(string)) (*plugins.Manager, *consoleCommands, error) {
	manager, err := plugins.New(e.Config.Dir, p.Profile.Dir, nil, notice)
	if err != nil {
		notice(pluginNoticePrefix + "Configuration error; keeping installed plugins: " + err.Error())
	} else if err := manager.Startup(ctx); err != nil {
		return nil, nil, err
	}
	commands := newConsoleCommands(plugins.Handles, func(ctx context.Context, line string) {
		if manager == nil {
			var err error
			manager, err = plugins.New(e.Config.Dir, p.Profile.Dir, nil, notice)
			if err != nil {
				notice(pluginNoticePrefix + "Configuration error: " + err.Error())
				return
			}
		}
		manager.Command(ctx, line)
	}, notice)
	return manager, commands, nil
}
