package egg

import (
	"context"
	"fmt"
	"sync/atomic"
)

const pluginNoticePrefix = "SourceEgg | Plugins | "

type consoleCommands struct {
	queue   chan string
	dropped atomic.Uint64
	handles func(string) bool
	command func(context.Context, string)
	notice  func(string)
}

func newConsoleCommands(handles func(string) bool, command func(context.Context, string), notice func(string)) *consoleCommands {
	return &consoleCommands{queue: make(chan string, 32), handles: handles, command: command, notice: notice}
}

func (c *consoleCommands) intercept(line string) bool {
	if !c.handles(line) {
		return false
	}
	select {
	case c.queue <- line:
	default:
		c.dropped.Add(1)
	}
	return true
}

func (c *consoleCommands) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case line := <-c.queue:
			if ctx.Err() != nil {
				return
			}
			if n := c.dropped.Swap(0); n > 0 {
				c.notice(fmt.Sprintf("%scommand queue full; %d commands discarded; retry when the current check finishes", pluginNoticePrefix, n))
			}
			c.command(ctx, line)
		}
	}
}
