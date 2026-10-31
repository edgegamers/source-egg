package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const commandHelp = "Commands: egg plugins list | add <plugin JSON> | check [id] | preview <id> | approve <id> <package-id> [replace] | adopt <id> <package-id> | keep <id> <path> | rollback <id> <previous-package-id> [replace] | uninstall <id> [confirm] | cancel <id>. Edit .source/plugins.json for all options."

// Command is called by the console's bounded worker, never from its input lock.
func (m *Manager) Command(ctx context.Context, line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != "egg" || f[1] != "plugins" {
		m.emit(commandHelp)
		return
	}
	if f[2] == "help" {
		m.emit(commandHelp)
		return
	}
	if e := m.reload(); e != nil {
		m.emit("Configuration error: " + e.Error())
		return
	}
	e := m.command(ctx, f, line)
	if e != nil {
		m.emit("Error: " + e.Error())
	}
}
func (m *Manager) command(ctx context.Context, f []string, line string) error {
	switch f[2] {
	case "cancel":
		if len(f) != 4 {
			return errors.New("usage: egg plugins cancel <id>")
		}
		if _, ok := m.state.Pending[f[3]]; !ok {
			return errors.New("no pending action for this plugin")
		}
		delete(m.state.Pending, f[3])
		if e := m.save(); e != nil {
			return e
		}
		m.emit(f[3] + ": pending action cancelled; installed files retained")
		return nil
	case "list":
		if len(f) != 3 {
			return errors.New("usage: egg plugins list")
		}
		installed, e := m.installer.Installed()
		if e != nil {
			return e
		}
		ids := map[string]bool{}
		for _, p := range m.manifest.Plugins {
			ids[p.ID] = true
		}
		for id := range installed {
			ids[id] = true
		}
		order := make([]string, 0, len(ids))
		for id := range ids {
			order = append(order, id)
		}
		sort.Strings(order)
		if len(order) == 0 {
			m.emit("No plugins registered. Edit .source/plugins.json or use egg plugins add <plugin JSON>.")
		}
		for _, id := range order {
			p, tracked := m.plugin(id)
			s := id + ": installed=" + installed[id].Current.Version
			if !tracked {
				s += " (untracked; files retained)"
			} else if p.Pin != "" {
				s += " pin=" + p.Pin
			}
			if tracked && p.Build != nil {
				ref := p.Source.Ref
				if ref == "" {
					ref = "main"
				}
				s += " source=" + p.Source.Repository + " ref=" + ref
			}
			if a, ok := m.state.Available[id]; ok {
				s += " available=" + a.Version + " package=" + a.ID
			}
			if a, ok := m.state.Pending[id]; ok {
				s += " pending=" + a.Kind + " " + a.Snapshot.Version
			}
			m.emit(s)
			for _, old := range installed[id].Previous {
				m.emit(id + ": rollback " + old.Version + " package=" + old.ID)
			}
		}
		return nil
	case "add":
		// Preserve JSON whitespace; the panel command prefix is three words.
		raw := strings.TrimSpace(line)
		for n := 0; n < 3; n++ {
			i := strings.IndexAny(raw, " \t")
			if i < 0 {
				return errors.New("usage: egg plugins add <plugin JSON>")
			}
			raw = strings.TrimSpace(raw[i:])
		}
		var p Plugin
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if e := d.Decode(&p); e != nil {
			return fmt.Errorf("invalid plugin recipe: %w", e)
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("provide one plugin object")
		}
		if e := ValidatePlugin(p); e != nil {
			return e
		}
		if _, ok := m.plugin(p.ID); ok {
			return errors.New("plugin already exists; edit its recipe in .source/plugins.json")
		}
		m.manifest.Plugins = append(m.manifest.Plugins, p)
		if e := m.manifest.Validate(); e != nil {
			return e
		}
		if e := m.writeManifest(); e != nil {
			return e
		}
		m.emit(p.ID + ": registered; run egg plugins check " + p.ID + " then preview the mapping")
		return nil
	case "check":
		if len(f) > 4 {
			return errors.New("usage: egg plugins check [id]")
		}
		id := ""
		if len(f) == 4 {
			id = f[3]
			if _, ok := m.plugin(id); !ok {
				return errors.New("unknown plugin")
			}
		}
		m.check(ctx, id)
		return nil
	case "preview":
		if len(f) != 4 {
			return errors.New("usage: egg plugins preview <id>")
		}
		s, ok := m.state.Available[f[3]]
		if !ok {
			return errors.New("no staged update; run egg plugins check first")
		}
		if p, ok := m.plugin(f[3]); !ok || s.Recipe != recipeHash(p) {
			return errors.New("recipe changed; run egg plugins check again")
		}
		m.emit(f[3] + ": " + s.Version + " package=" + s.ID)
		paths := make([]string, 0, len(s.Files))
		for name := range s.Files {
			paths = append(paths, name)
		}
		sort.Strings(paths)
		for _, name := range paths {
			action := "manage"
			if s.Files[name].Seed {
				action = "seed only / preserve existing"
			}
			m.emit(action + " " + name)
		}
		changes, e := m.installer.Preview(f[3], &s, false)
		for _, c := range changes {
			m.emit(c.Action + " " + c.Path)
		}
		m.emit("To install at next restart: egg plugins approve " + f[3] + " " + s.ID)
		if e != nil {
			return fmt.Errorf("installation is blocked: %w", e)
		}
		return nil
	case "approve", "update", "adopt":
		if len(f) < 5 || len(f) > 6 || (len(f) == 6 && (f[5] != "replace" || f[2] == "adopt")) {
			return errors.New("usage: egg plugins approve <id> <exact-package-id> [replace], or adopt <id> <exact-package-id>")
		}
		p, ok := m.plugin(f[3])
		if !ok {
			return errors.New("unknown plugin")
		}
		s, ok := m.state.Available[p.ID]
		if !ok || s.ID != f[4] {
			return errors.New("approval requires the exact staged package ID from preview")
		}
		if s.Recipe != recipeHash(p) {
			return errors.New("recipe changed; check and preview again")
		}
		if f[2] == "adopt" {
			if e := m.installer.Adopt(p.ID, s); e != nil {
				return e
			}
			delete(m.state.Available, p.ID)
			delete(m.state.Pending, p.ID)
			if e := m.save(); e != nil {
				return e
			}
			m.emit(p.ID + ": existing matching files adopted; no game files changed")
			return nil
		}
		replace := len(f) == 6
		if _, e := m.installer.Preview(p.ID, &s, replace); e != nil {
			return e
		}
		m.state.Pending[p.ID] = pendingAction{Kind: "install", Snapshot: s, Recipe: recipeHash(p), Replace: replace}
		if e := m.save(); e != nil {
			return e
		}
		m.emit(p.ID + ": approved " + s.Version + " (" + s.ID + "); will install at next normal restart")
		return nil
	case "keep":
		if len(f) != 5 || !ValidTarget(f[4]) {
			return errors.New("usage: egg plugins keep <id> <game-relative-path>")
		}
		for n, p := range m.manifest.Plugins {
			if p.ID == f[3] {
				p.Preserve = append(p.Preserve, f[4])
				m.manifest.Plugins[n] = p
				if e := m.writeManifest(); e != nil {
					return e
				}
				delete(m.state.Pending, p.ID)
				if e := m.save(); e != nil {
					return e
				}
				m.emit(p.ID + ": preserving " + f[4] + "; check and approve again")
				return nil
			}
		}
		return errors.New("unknown plugin")
	case "rollback":
		if len(f) < 5 || len(f) > 6 || (len(f) == 6 && f[5] != "replace") {
			return errors.New("usage: egg plugins rollback <id> <previous-package-id> [replace]")
		}
		p, ok := m.plugin(f[3])
		if !ok {
			return errors.New("restore the plugin recipe before rollback")
		}
		installed, e := m.installer.Installed()
		if e != nil {
			return e
		}
		for _, s := range installed[p.ID].Previous {
			if s.ID == f[4] {
				replace := len(f) == 6
				changes, e := m.installer.Preview(p.ID, &s, replace)
				if e != nil {
					return e
				}
				for _, c := range changes {
					m.emit(c.Action + " " + c.Path)
				}
				m.state.Pending[p.ID] = pendingAction{Kind: "rollback", Snapshot: s, Recipe: recipeHash(p), Replace: replace}
				if e := m.save(); e != nil {
					return e
				}
				m.emit(p.ID + ": rollback to " + s.Version + " queued for next restart")
				return nil
			}
		}
		return errors.New("unknown rollback package; run egg plugins list")
	case "uninstall":
		if len(f) < 4 || len(f) > 5 || (len(f) == 5 && f[4] != "confirm") {
			return errors.New("usage: egg plugins uninstall <id> [confirm]")
		}
		installed, e := m.installer.Installed()
		if e != nil {
			return e
		}
		i, ok := installed[f[3]]
		if !ok {
			return errors.New("plugin is not managed")
		}
		changes, e := m.installer.Preview(f[3], nil, false)
		if e != nil {
			return e
		}
		for _, c := range changes {
			m.emit(c.Action + " " + c.Path)
		}
		if len(f) == 4 {
			m.emit("To remove these eligible files at next restart: egg plugins uninstall " + f[3] + " confirm")
			return nil
		}
		// Freeze the removal preview: later recipe removal or a reverted edit
		// must never make an approved uninstall more destructive.
		var preserve []string
		for _, change := range changes {
			if change.Action != "remove" {
				preserve = append(preserve, change.Path)
			}
		}
		m.state.Pending[f[3]] = pendingAction{Kind: "uninstall", Snapshot: i.Current, Preserve: preserve}
		if e := m.save(); e != nil {
			return e
		}
		m.emit(f[3] + ": uninstall queued for next restart; remove its recipe to stop update notices")
		return nil
	default:
		return errors.New(commandHelp)
	}
}
