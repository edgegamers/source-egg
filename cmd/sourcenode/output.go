package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/content"
	"github.com/edgegamers/source-egg/internal/metrics"
	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/observability"
	"github.com/edgegamers/source-egg/internal/operations"
	"github.com/edgegamers/source-egg/internal/passwords"
	"github.com/edgegamers/source-egg/internal/terminalui"
)

func shortID(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}
func textOrDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
func enabled(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
func when(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format("2006-01-02 15:04 UTC")
}
func size(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	amount := float64(value) / 1024
	index := 0
	for amount >= 1024 && index < len(units)-1 {
		amount /= 1024
		index++
	}
	return fmt.Sprintf("%.1f %s", amount, units[index])
}
func generation(value string) string {
	if value == "" {
		return "—"
	}
	return path.Base(value)
}
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Human output uses an explicit schema per command. Unexpected JSON fields are
// deliberately ignored; passwords are shown only by the dedicated reveal path.
// JSON mode preserves valid API responses byte-for-byte, including large ints.
func renderResponse(out io.Writer, command string, data []byte, jsonOutput bool) error {
	command = strings.TrimPrefix(strings.SplitN(command, "?", 2)[0], "/")
	if jsonOutput {
		if json.Valid(data) {
			_, err := fmt.Fprintln(out, string(bytes.TrimSpace(data)))
			return err
		}
		key := "message"
		if command == "crash" {
			key = "tail"
		}
		return json.NewEncoder(out).Encode(map[string]string{key: string(data)})
	}
	ui := terminalui.New(out)
	if command == "crash" {
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			fmt.Fprintln(out, terminalui.Plain(line))
		}
		return nil
	}
	if !json.Valid(data) {
		switch command {
		case "pin", "unpin", "rollback", "unpin-game", "prune":
			ui.Step(strings.TrimSpace(string(data)))
			return nil
		}
		return fmt.Errorf("invalid %s response", command)
	}
	switch command {
	case "status":
		var response struct {
			Servers []statusServer `json:"servers"`
			Modules moduleView     `json:"modules"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		ui.Rule("Servers")
		sort.Slice(response.Servers, func(i, j int) bool { return response.Servers[i].Server < response.Servers[j].Server })
		rows := [][]string{}
		for _, server := range response.Servers {
			state := "stopped"
			if server.Running {
				state = "starting"
				if server.State.Ready {
					state = "running"
				}
			}
			rows = append(rows, []string{shortID(server.Server), textOrDash(server.State.Name), server.Game, state, strconv.Itoa(server.State.Players), textOrDash(server.State.Map), generation(server.Reference.Current), generation(server.Pending)})
		}
		ui.Table([]string{"Server", "Name", "Game", "State", "Players", "Map", "Build", "Pending"}, rows)
		renderModules(ui, response.Modules)
	case "modules":
		var response moduleView
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		renderModules(ui, response)
	case "doctor":
		return renderDoctor(ui, data)
	case "backups", "backup":
		var records []operations.BackupRecord
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].ServerID != records[j].ServerID {
				return records[i].ServerID < records[j].ServerID
			}
			return records[i].Time.After(records[j].Time)
		})
		rows := [][]string{}
		indices := map[string]int{}
		for _, record := range records {
			indices[record.ServerID]++
			rows = append(rows, []string{shortID(record.ServerID), strconv.Itoa(indices[record.ServerID]), record.Game, when(record.Time), strconv.Itoa(record.Files), size(record.Bytes)})
		}
		ui.Rule("Backups")
		ui.Table([]string{"Server", "Index", "Game", "Created", "Files", "Size"}, rows)
	case "logs":
		var records []observability.LogEntry
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		rows := [][]string{}
		for _, record := range records {
			rows = append(rows, []string{shortID(record.UUID), when(record.Date), record.Name, size(record.Bytes), yesNo(record.Compressed)})
		}
		ui.Rule("Logs")
		ui.Table([]string{"Server", "Date", "File", "Size", "Compressed"}, rows)
	case "crashes":
		var records []observability.Bundle
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].UUID != records[j].UUID {
				return records[i].UUID < records[j].UUID
			}
			return records[i].Time.After(records[j].Time)
		})
		rows := [][]string{}
		indices := map[string]int{}
		for _, record := range records {
			indices[record.UUID]++
			rows = append(rows, []string{shortID(record.UUID), strconv.Itoa(indices[record.UUID]), when(record.Time), record.Game, textOrDash(record.Map), strconv.Itoa(record.ExitCode), size(record.Bytes)})
		}
		ui.Rule("Crashes")
		ui.Table([]string{"Server", "Index", "Time", "Game", "Map", "Exit", "Size"}, rows)
	case "cleanup":
		return renderCleanup(ui, data)
	case "maintenance", "maintenance/setup":
		return renderMaintenance(ui, data)
	case "pins", "frameworks", "framework-pin":
		return renderPins(ui, data)
	case "metrics", "top":
		return renderMetrics(ui, data)
	case "history", "update":
		var records []content.Generation
		if command == "history" {
			if err := json.Unmarshal(data, &records); err != nil {
				return err
			}
		} else {
			var record content.Generation
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		ui.Rule("Game generations")
		rows := [][]string{}
		for _, record := range records {
			rows = append(rows, []string{record.Identity.Game, record.Identity.Branch, record.BuildID, record.Key})
		}
		ui.Table([]string{"Game", "Branch", "Build", "Generation"}, rows)
	case "password":
		var record passwords.Assignment
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		ui.Rule("Generated join password")
		ui.KV("Password", record.Password)
		ui.KV("Generated", when(record.GeneratedAt))
		next := when(record.NextAt)
		if record.NextAt.IsZero() {
			next = "rotation disabled"
		}
		ui.KV("Next rotation", next)
	case "restore":
		var response struct {
			Restored string `json:"restored"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		ui.Step("Restored backup for " + shortID(response.Restored))
	case "self-update":
		var response struct{ Version string }
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		ui.Rule("Node update")
		ui.KV("Available version", response.Version)
	default:
		return fmt.Errorf("no human renderer for %s; use --json", command)
	}
	return nil
}

type statusServer struct {
	Server, Game string
	Running      bool
	State        node.State
	Reference    node.Reference
	Pending      string
}

type moduleView struct {
	Version, Channel  string
	MetricsEnabled    bool  `json:"metrics_enabled"`
	SelfUpdateEnabled bool  `json:"self_update_enabled"`
	FrameworkCache    bool  `json:"framework_cache"`
	PasswordEnabled   *bool `json:"password_enabled"`
	MaintenanceDryRun bool  `json:"maintenance_dry_run"`
	Operations        operations.Status
	Observability     observability.Status
	NextScheduled     map[string]time.Time `json:"next_scheduled"`
	LastNodeUpdate    struct {
		Version   string
		Updated   bool
		CheckedAt time.Time `json:"checked_at"`
		Error     string
	} `json:"last_node_update"`
}

func renderModules(ui terminalui.UI, response moduleView) {
	ui.Rule("Node and modules")
	if response.Version != "" {
		ui.KV("Version", response.Version)
	}
	if response.Channel != "" {
		ui.KV("Channel", response.Channel)
	}
	rows := [][]string{{"Metrics", enabled(response.MetricsEnabled)}, {"Framework cache", enabled(response.FrameworkCache)}, {"Automatic node updates", enabled(response.SelfUpdateEnabled)}}
	if response.PasswordEnabled != nil {
		rows = append(rows, []string{"Generated passwords", enabled(*response.PasswordEnabled)})
	}
	ui.Table([]string{"Module", "State"}, rows)
	rows = nil
	for _, entry := range []struct {
		name, key string
		last      time.Time
	}{{"Backup", "backup", response.Operations.LastBackup}, {"Cleanup", "cleanup", response.Operations.LastCleanup}, {"Maintenance", "maintenance", response.Operations.LastMaintenance}} {
		next := "not scheduled"
		if at, ok := response.NextScheduled[entry.key]; ok {
			next = when(at)
		}
		rows = append(rows, []string{entry.name, when(entry.last), next})
	}
	ui.Table([]string{"Operation", "Last run", "Next run"}, rows)
	ui.KV("Maintenance mode", map[bool]string{true: "preview", false: "apply"}[response.MaintenanceDryRun])
	ui.KV("Queues", fmt.Sprintf("alerts %d, logs %d, crashes %d", response.Observability.AlertsQueued, response.Observability.LogsQueued, response.Observability.CrashesQueued))
	if response.Observability.LastError != "" {
		ui.KV("Last module error", response.Observability.LastError)
	}
	if !response.LastNodeUpdate.CheckedAt.IsZero() {
		ui.KV("Last update check", when(response.LastNodeUpdate.CheckedAt))
		if response.LastNodeUpdate.Error != "" {
			ui.KV("Update error", response.LastNodeUpdate.Error)
		}
	}
}

func renderDoctor(ui terminalui.UI, data []byte) error {
	var response struct {
		Checks  map[string]json.RawMessage
		Modules moduleView
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	ui.Rule("Doctor")
	rows := [][]string{}
	for _, check := range []struct{ key, label string }{{"docker", "Docker"}, {"wings", "Wings"}, {"alerts", "Alerts"}} {
		var value string
		if raw, ok := response.Checks[check.key]; ok {
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			rows = append(rows, []string{check.label, value})
		}
	}
	var steam struct {
		Path       string
		Executable bool
	}
	if raw := response.Checks["steamcmd"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &steam); err == nil {
			rows = append(rows, []string{"SteamCMD", steam.Path + " (executable: " + yesNo(steam.Executable) + ")"})
		} else {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			rows = append(rows, []string{"SteamCMD", value})
		}
	}
	var reboot bool
	_ = json.Unmarshal(response.Checks["reboot_required"], &reboot)
	rows = append(rows, []string{"Reboot required", yesNo(reboot)})
	var crashes int
	_ = json.Unmarshal(response.Checks["crashes_last_24h"], &crashes)
	rows = append(rows, []string{"Crashes in 24 hours", strconv.Itoa(crashes)})
	var disk struct {
		Available int64 `json:"available_bytes"`
		Total     int64 `json:"total_bytes"`
	}
	if raw := response.Checks["state_disk"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &disk); err != nil {
			return err
		}
		rows = append(rows, []string{"State disk", size(disk.Available) + " free / " + size(disk.Total)})
	}
	ui.Table([]string{"Check", "Result"}, rows)
	var mounts map[string]json.RawMessage
	if raw := response.Checks["mounts"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &mounts); err != nil {
			return err
		}
	}
	ui.Rule("Mounts")
	rows = nil
	for _, id := range sortedKeys(mounts) {
		var flags struct {
			Socket bool `json:"socket_read_only"`
			Shared bool `json:"shared_read_only"`
			Cache  bool `json:"cache_read_only"`
		}
		if err := json.Unmarshal(mounts[id], &flags); err == nil {
			rows = append(rows, []string{shortID(id), yesNo(flags.Socket), yesNo(flags.Shared), yesNo(flags.Cache), ""})
		} else {
			var message string
			if err := json.Unmarshal(mounts[id], &message); err != nil {
				return err
			}
			rows = append(rows, []string{shortID(id), "—", "—", "—", message})
		}
	}
	ui.Table([]string{"Server", "Socket RO", "Shared RO", "Cache RO", "Error"}, rows)
	renderModules(ui, response.Modules)
	return nil
}

func renderCleanup(ui terminalui.UI, data []byte) error {
	var records []operations.CleanupResult
	ui.Rule("Cleanup")
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
	} else {
		var response struct {
			operations.Status
			Last  *operations.CleanupResult `json:"last"`
			Total operations.CleanupResult  `json:"total"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		if response.Last != nil {
			records = append(records, *response.Last)
			ui.KV("Total reclaimed", size(response.Total.Bytes))
		} else {
			for _, id := range sortedKeys(response.Cleanup) {
				records = append(records, response.Cleanup[id])
			}
			ui.KV("Last run", when(response.LastCleanup))
			ui.KV("Total reclaimed", size(response.CleanupTotal.Bytes))
		}
	}
	rows := [][]string{}
	for _, record := range records {
		rows = append(rows, []string{shortID(record.ServerID), when(record.Time), strconv.Itoa(record.Files), size(record.Bytes), strconv.Itoa(record.Skipped), strconv.Itoa(len(record.Errors))})
	}
	ui.Table([]string{"Server", "Last run", "Removed", "Reclaimed", "Skipped", "Errors"}, rows)
	rules := [][]string{}
	for _, record := range records {
		for _, name := range sortedKeys(record.PerRule) {
			stat := record.PerRule[name]
			rules = append(rules, []string{shortID(record.ServerID), name, strconv.Itoa(stat.Files), size(stat.Bytes)})
		}
		for _, message := range record.Errors {
			ui.KV("Cleanup error", message)
		}
	}
	if len(rules) > 0 {
		ui.Table([]string{"Server", "Rule", "Removed", "Reclaimed"}, rules)
	}
	return nil
}

func renderMaintenance(ui terminalui.UI, data []byte) error {
	ui.Rule("Maintenance")
	var tasks []operations.TaskResult
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if err := json.Unmarshal(data, &tasks); err != nil {
			return err
		}
	} else {
		var response struct {
			operations.Status
			Setup string `json:"setup"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		if response.Setup != "" {
			ui.Step(response.Setup)
			return nil
		}
		tasks = response.Maintenance
		ui.KV("Last run", when(response.LastMaintenance))
		ui.KV("Runs", strconv.FormatInt(response.MaintenanceRuns, 10))
		ui.KV("Total reclaimed", size(response.MaintenanceBytes))
	}
	rows := [][]string{}
	for _, task := range tasks {
		mode := "applied"
		if task.Dry {
			mode = "preview"
		}
		note := task.Note
		if task.Error != "" {
			mode = "failed"
			note = task.Error
		}
		rows = append(rows, []string{task.Task, mode, strconv.Itoa(task.Items), size(task.Bytes), textOrDash(note)})
	}
	ui.Table([]string{"Task", "Result", "Items", "Bytes", "Details"}, rows)
	return nil
}

func renderPins(ui terminalui.UI, data []byte) error {
	var response struct {
		Policy  node.FrameworkPolicy
		Servers []node.FrameworkView
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	ui.Rule("Fleet pins")
	rows := [][]string{}
	for _, framework := range []string{"metamod", "sourcemod"} {
		pin := response.Policy.Fleet[framework]
		if pin == "" {
			pin = "latest"
		}
		rows = append(rows, []string{framework, pin})
	}
	ui.Table([]string{"Framework", "Selected"}, rows)
	ui.Rule("Server frameworks")
	rows = nil
	sort.Slice(response.Servers, func(i, j int) bool { return response.Servers[i].Server < response.Servers[j].Server })
	for _, server := range response.Servers {
		for _, framework := range []string{"metamod", "sourcemod"} {
			rows = append(rows, []string{shortID(server.Server), server.Game, framework, textOrDash(server.Installed[framework]), textOrDash(server.Selected[framework])})
		}
	}
	ui.Table([]string{"Server", "Game", "Framework", "Installed", "Selected"}, rows)
	return nil
}

func renderMetrics(ui terminalui.UI, data []byte) error {
	var response metrics.Snapshot
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	ui.Rule("Metrics")
	ui.KV("Sampled", when(response.At))
	ui.KV("Host CPU", fmt.Sprintf("%.1f%%", response.Host.CPU))
	ui.KV("Host memory", size(int64(response.Host.Memory))+" / "+size(int64(response.Host.Limit)))
	rows := [][]string{}
	for _, server := range response.Servers {
		state := "stopped"
		if server.Running {
			state = "running"
		}
		if server.Error != "" {
			state = server.Error
		}
		rows = append(rows, []string{shortID(server.ID), server.Name, server.Game, strconv.Itoa(server.Players), fmt.Sprintf("%.1f%%", server.Sample.CPU), size(int64(server.Sample.Memory)), state})
	}
	ui.Table([]string{"Server", "Name", "Game", "Players", "CPU", "Memory", "State"}, rows)
	return nil
}
