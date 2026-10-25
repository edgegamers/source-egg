package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}
type embed struct {
	Title     string            `json:"title"`
	Timestamp string            `json:"timestamp"`
	Color     int               `json:"color"`
	Fields    []embedField      `json:"fields,omitempty"`
	Author    map[string]string `json:"author,omitempty"`
	Footer    map[string]string `json:"footer,omitempty"`
}
type payload struct {
	Username        string              `json:"username,omitempty"`
	AvatarURL       string              `json:"avatar_url,omitempty"`
	Embeds          []embed             `json:"embeds"`
	AllowedMentions map[string][]string `json:"allowed_mentions"`
}

var titles = map[string]string{"crash": "Server crashed", "game.update": "Game updated", "game.update.failed": "Game update failed", "framework.update": "Framework updated", "push.failed": "Content push failed", "restart.deferred": "Restart deferred", "node.update": "Source node updated", "backup.done": "Backup done", "backup.failed": "Backup failed", "cleanup.done": "Cleanup done", "maintenance.done": "Maintenance done", "disk.low": "Disk almost full", "reboot.required": "Reboot required", "server.start": "Server up", "server.stop": "Server down", "server.restart": "Server restarting"}

func (m *Manager) render(n Notice) embed {
	e := embed{Title: titles[n.Kind], Timestamp: n.Time.UTC().Format(time.RFC3339), Color: 0x3498DB}
	if strings.Contains(n.Kind, "failed") || n.Kind == "crash" {
		e.Color = 0xE74C3C
	}
	if n.Server.Name != "" {
		e.Author = map[string]string{"name": n.Server.Name}
	}
	footer := m.clean(m.cfg.Alerts.Name, 128)
	if n.Server.UUID != "" {
		footer = strings.TrimSpace(footer + " · " + truncate(n.Server.UUID, 12))
	}
	if footer != "" {
		e.Footer = map[string]string{"text": footer}
	}
	fields := map[string]string{}
	for k, v := range n.Fields {
		fields[k] = v
	}
	if n.Server.Game != "" {
		fields["Game"] = n.Server.Game
	}
	if n.Server.Map != "" {
		fields["Map"] = n.Server.Map
	}
	if n.Server.UUID != "" {
		fields["Players"] = strconv.Itoa(n.Server.Players)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	budget := 1800
	for _, k := range keys {
		if len(e.Fields) >= 12 || budget <= 0 {
			break
		}
		v := truncate(fields[k], min(256, budget))
		if v != "" {
			e.Fields = append(e.Fields, embedField{Name: truncate(k, 64), Value: v, Inline: true})
			budget -= len([]rune(v)) + len([]rune(k))
		}
	}
	return e
}
func (m *Manager) runAlerts(ctx context.Context) {
	for {
		var first Notice
		select {
		case <-ctx.Done():
			return
		case first = <-m.notices:
		}
		pending := []Notice{first}
		timer := m.opts.After(aggregateDelay)
	collect:
		for {
			var input <-chan Notice = m.notices
			if len(pending) >= m.cfg.Alerts.QueueSize {
				input = nil
			}
			select {
			case <-ctx.Done():
				m.record(nil, func(s *Status) { s.AlertsDropped += uint64(len(pending)) })
				return
			case n := <-input:
				pending = append(pending, n)
			case <-timer:
				break collect
			}
		}
		for len(pending) > 0 {
			p := payload{Username: m.clean(m.cfg.Alerts.Username, 80), AvatarURL: m.cfg.Alerts.AvatarURL, AllowedMentions: map[string][]string{"parse": {}}}
			count := 0
			size := 0
			for count < len(pending) && count < 10 {
				e := m.render(pending[count])
				b, _ := json.Marshal(e)
				if count > 0 && size+len(b) > 5800 {
					break
				}
				size += len(b)
				p.Embeds = append(p.Embeds, e)
				count++
			}
			e := m.post(ctx, p)
			m.record(e, func(s *Status) {
				if e == nil {
					s.AlertsSent++
				} else {
					s.AlertsFailed++
				}
			})
			pending = pending[count:]
			if ctx.Err() != nil {
				m.record(nil, func(s *Status) { s.AlertsDropped += uint64(len(pending)) })
				return
			}
		}
	}
}
func (m *Manager) post(ctx context.Context, p payload) error {
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.Alerts.Webhook, bytes.NewReader(b))
		if e != nil {
			return errors.New("invalid webhook request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "sourcenode")
		resp, e := m.opts.HTTP.Do(req)
		if e != nil {
			return errors.New("webhook transport failed")
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode != 429 {
			return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
		}
		var rate struct {
			RetryAfter float64 `json:"retry_after"`
		}
		json.Unmarshal(body, &rate)
		seconds := rate.RetryAfter
		if seconds <= 0 {
			seconds, _ = strconv.ParseFloat(resp.Header.Get("Retry-After"), 64)
		}
		if seconds <= 0 {
			seconds = 1
		}
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			seconds = 1
		}
		// Saturate only at time.Duration's representable ceiling, rather than
		// retrying earlier than Discord requested. Cancellation interrupts this.
		seconds = min(seconds, float64((1<<63-1)/int64(time.Second))-1)
		wait := time.Duration(seconds*float64(time.Second)) + 100*time.Millisecond
		if attempt == 0 {
			m.record(nil, func(s *Status) { s.AlertsRetried++ })
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.opts.After(wait):
		}
	}
	return errors.New("webhook rate limited")
}

// CheckWebhook checks identity with a GET; it never posts or returns the URL.
func (m *Manager) CheckWebhook(ctx context.Context) (string, error) {
	if !m.cfg.Alerts.Enabled {
		return "", errors.New("alerts disabled")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.Alerts.Webhook, nil)
	if e != nil {
		return "", errors.New("invalid webhook request")
	}
	resp, e := m.opts.HTTP.Do(req)
	if e != nil {
		return "", errors.New("webhook transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	var reply struct {
		Name string `json:"name"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reply); e != nil {
		return "", errors.New("invalid webhook reply")
	}
	return m.clean(reply.Name, 128), nil
}
