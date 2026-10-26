package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/observability"
	"github.com/edgegamers/source-egg/internal/passwords"
)

func post(ctx context.Context, hook string, fields any) error {
	if hook == "" {
		return nil
	}
	b, e := json.Marshal(fields)
	if e != nil {
		return e
	}
	body, _ := json.Marshal(map[string]string{"username": "sourcenode", "content": "```json\n" + string(b) + "\n```"})
	if len(body) > 1900 {
		return errors.New("webhook payload too large")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("webhook redirects refused") }}
	for attempt := 0; attempt < 2; attempt++ {
		req, e := http.NewRequestWithContext(ctx, "POST", hook, bytes.NewReader(body))
		if e != nil {
			return e
		}
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			return e
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode != 429 {
			return errors.New("webhook delivery rejected")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("webhook rate limit")
}
func (d *Daemon) notice(ctx context.Context, event string, s Server, fields map[string]any) {
	if d.Observation == nil {
		return
	}
	if event == "server.crash" {
		event = "crash"
	}
	clean := stringifyFields(fields)
	for key, value := range clean {
		clean[key] = redactServer(s, value)
	}
	d.Observation.Notify(observability.Notice{Kind: event, Server: d.observationServer(s), Fields: clean})
}
func (d *Daemon) deliverPassword(ctx context.Context, s Server, scope string, a passwords.Assignment) {
	d.notify.Lock()
	defer d.notify.Unlock()
	root, e := os.OpenRoot(d.Config.StateDir)
	if e != nil {
		return
	}
	defer root.Close()
	sent := map[string]bool{}
	if e = fsx.ReadJSON(root, "password-notifications.json", &sent); e != nil && !os.IsNotExist(e) {
		return
	}
	if sent[a.Revision] {
		return
	}
	if post(ctx, d.Config.PasswordWebhook, map[string]any{"event": "password.generated", "scope": scope, "server": s.UUID, "game": s.Identity.Game, "password": a.Password, "next_at": a.NextAt}) != nil {
		return
	}
	sent[a.Revision] = true
	_ = fsx.JSON(root, "password-notifications.json", sent, 0600)
}
