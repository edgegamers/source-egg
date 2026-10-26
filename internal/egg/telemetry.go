package egg

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/node"
)

type consoleDelivery struct {
	egg      *Egg
	identity game.Identity
	queue    chan string
	dropped  atomic.Uint64
	report   func(string)
}

func newConsoleDelivery(e *Egg, id game.Identity, report func(string)) *consoleDelivery {
	return &consoleDelivery{egg: e, identity: id, queue: make(chan string, 512), report: report}
}
func (d *consoleDelivery) offer(line string) {
	if len(line) > 4000 {
		line = line[:4000]
	}
	select {
	case d.queue <- line:
	default:
		d.dropped.Add(1)
	}
}
func (d *consoleDelivery) run(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastWarning := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		lines := []string{}
		for len(lines) < 32 {
			select {
			case line := <-d.queue:
				lines = append(lines, line)
			default:
				goto send
			}
		}
	send:
		if len(lines) > 0 {
			requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, e := d.egg.call(requestCtx, node.Request{Action: "logs", Identity: d.identity, Console: lines})
			cancel()
			if e != nil {
				d.dropped.Add(uint64(len(lines)))
			}
		}
		if time.Since(lastWarning) > 30*time.Second {
			if n := d.dropped.Swap(0); n > 0 && d.report != nil {
				lastWarning = time.Now()
				d.report(fmt.Sprintf("SourceEgg | Node console delivery dropped %d lines", n))
			}
		}
	}
}

type consoleWriter struct{ line func(string) }

func (w consoleWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimSuffix(string(p), "\n"), "\n") {
		w.line(line)
	}
	return len(p), nil
}
