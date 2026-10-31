package plugins

func (m *Manager) autoApprove(p Plugin) bool {
	if p.AutoApprove != nil {
		return *p.AutoApprove
	}
	return m.manifest.AutoApprove
}

// Policy edits cancel only automatic work. Explicit actions retain their exact
// snapshot and are still validated against the content recipe at installation.
func (m *Manager) pruneAutomatic() error {
	changed := false
	for id, action := range m.state.Pending {
		if !action.Automatic {
			continue
		}
		p, tracked := m.plugin(id)
		if !tracked || !m.autoApprove(p) || action.Recipe != recipeHash(p) {
			delete(m.state.Pending, id)
			changed = true
			m.emit(id + ": automatic install cancelled because its policy or recipe changed")
		}
	}
	if changed {
		return m.save()
	}
	return nil
}

func (m *Manager) clearAutomatic(id string) {
	if action, ok := m.state.Pending[id]; ok && action.Automatic {
		delete(m.state.Pending, id)
	}
}

// Queue only verified staging and retain all existing ownership and edit
// checks. Valid manual approvals, rollback and uninstall take precedence.
func (m *Manager) queueAutomatic(p Plugin, snapshot Snapshot) {
	if !m.autoApprove(p) || snapshot.Recipe != recipeHash(p) {
		return
	}
	pending, exists := m.state.Pending[p.ID]
	if exists && !pending.Automatic {
		if pending.Kind == "uninstall" || pending.Recipe == snapshot.Recipe {
			return
		}
		// A content edit (including a changed pin) already invalidates this
		// explicit approval. It must not delay the newly eligible auto build.
		delete(m.state.Pending, p.ID)
		exists = false
		m.emit(p.ID + ": approval cancelled because its recipe changed")
	}
	if _, err := m.installer.Preview(p.ID, &snapshot, false); err != nil {
		m.clearAutomatic(p.ID)
		m.emit(p.ID + ": automatic install blocked; keeping installed files: " + err.Error())
		return
	}
	if exists && pending.Snapshot.ID == snapshot.ID {
		return
	}
	m.state.Pending[p.ID] = pendingAction{Kind: "install", Automatic: true, Snapshot: snapshot, Recipe: snapshot.Recipe}
	m.emit(p.ID + ": automatic install queued " + snapshot.Version + " (" + snapshot.ID + "); will install at next normal restart")
}
