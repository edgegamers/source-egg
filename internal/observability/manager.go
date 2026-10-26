package observability

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Server struct {
	UUID     string            `json:"uuid"`
	Volume   string            `json:"-"`
	Game     string            `json:"game"`
	Name     string            `json:"name"`
	Map      string            `json:"map"`
	Players  int               `json:"players"`
	Versions map[string]string `json:"versions,omitempty"`
}
type Notice struct {
	Kind   string
	Server Server
	Fields map[string]string
	Time   time.Time
}
type CrashReport struct {
	ExitCode int
	Lines    []string
	Time     time.Time
}
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// After and Now are clock seams. After must return a fresh channel for each call.
// Redact should replace every configured credential before it reaches storage.
type Options struct {
	HTTP   HTTPDoer
	Now    func() time.Time
	After  func(time.Duration) <-chan time.Time
	Redact func(string) string
}
type Status struct {
	AlertsQueued       int    `json:"alerts_queued"`
	LogsQueued         int    `json:"logs_queued"`
	CrashesQueued      int    `json:"crashes_queued"`
	AlertsSent         uint64 `json:"alerts_sent"`
	AlertsFailed       uint64 `json:"alerts_failed"`
	AlertsDropped      uint64 `json:"alerts_dropped"`
	AlertsRetried      uint64 `json:"alerts_retried"`
	LogsWritten        uint64 `json:"logs_written"`
	LogsFailed         uint64 `json:"logs_failed"`
	LogsDropped        uint64 `json:"logs_dropped"`
	CrashesWritten     uint64 `json:"crashes_written"`
	CrashesFailed      uint64 `json:"crashes_failed"`
	CrashesDropped     uint64 `json:"crashes_dropped"`
	CrashesRateLimited uint64 `json:"crashes_rate_limited"`
	LastError          string `json:"last_error,omitempty"`
}
type logItem struct {
	s          Server
	kind, line string
	at         time.Time
}
type crashItem struct {
	s Server
	r CrashReport
}
type Manager struct {
	cfg             Config
	opts            Options
	notices         chan Notice
	logs            chan logItem
	crashes         chan crashItem
	mu              sync.Mutex
	st              Status
	running, closed bool
	lastCrash       map[string]time.Time
	disk            sync.Mutex
}

func New(c Config, o Options) (*Manager, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if o.Redact == nil {
		o.Redact = func(s string) string { return s }
	}
	c.Alerts.Events = slices.Clone(c.Alerts.Events)
	m := &Manager{cfg: c, opts: o, notices: make(chan Notice, c.Alerts.QueueSize), logs: make(chan logItem, c.Logs.QueueSize), crashes: make(chan crashItem, c.Crashes.QueueSize), lastCrash: map[string]time.Time{}}
	for _, d := range []struct {
		enabled bool
		path    string
	}{{c.Logs.Enabled, c.Logs.Dir}, {c.Crashes.Enabled, c.Crashes.Dir}} {
		if d.enabled {
			r, e := privateRoot(d.path)
			if e != nil {
				return nil, e
			}
			r.Close()
		}
	}
	// Seed the crash-loop guard from durable bundles, preserving it on restart.
	if c.Crashes.Enabled {
		r, e := privateRoot(c.Crashes.Dir)
		if e != nil {
			return nil, e
		}
		e = serverRoots(r, "", func(id string, server *os.Root) error {
			bundles, err := readBundles(context.Background(), server, id)
			if err != nil {
				return err
			}
			if len(bundles) > 0 && o.Now().Sub(bundles[0].Time) < time.Minute && len(m.lastCrash) < 65536 {
				m.lastCrash[id] = bundles[0].Time
			}
			return nil
		})
		r.Close()
		if e != nil {
			return nil, e
		}
	}
	return m, nil
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var credentials = regexp.MustCompile(`(?i)(\b(?:password|passwd|rcon_password|sv_password|token|gslt|sv_setsteamaccount|authorization|api[_-]?key)\b\s*(?:[=:]\s*|\s+))(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)
var terminalSequences = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|[@-_])")

// Sanitize removes terminal control sequences and common inline credentials.
// It cannot discover arbitrary secret values: use Options.Redact for those.
func Sanitize(s string) string {
	s = credentials.ReplaceAllString(s, "${1}[REDACTED]")
	s = terminalSequences.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, s)
	return credentials.ReplaceAllString(s, "${1}[REDACTED]")
}
func truncate(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}
func (m *Manager) clean(s string, n int) string {
	return truncate(Sanitize(m.opts.Redact(truncate(s, n*4))), n)
}
func (m *Manager) server(s Server) Server {
	s.Name = m.clean(s.Name, 256)
	s.Game = m.clean(s.Game, 64)
	s.Map = m.clean(s.Map, 256)
	versions := map[string]string{}
	for k, v := range s.Versions {
		if len(versions) >= 32 {
			break
		}
		versions[m.clean(k, 64)] = m.clean(v, 256)
	}
	s.Versions = versions
	return s
}
func (m *Manager) Notify(n Notice) bool {
	if !m.cfg.Alerts.Enabled || !slices.Contains(eventKinds, n.Kind) || !(len(m.cfg.Alerts.Events) == 0 || slices.Contains(m.cfg.Alerts.Events, "all") || slices.Contains(m.cfg.Alerts.Events, n.Kind)) {
		return false
	}
	if n.Server.UUID != "" && !idPattern.MatchString(n.Server.UUID) {
		return false
	}
	n.Server = m.server(n.Server)
	n.Server.Volume = ""
	if n.Time.IsZero() {
		n.Time = m.opts.Now()
	}
	fields := map[string]string{}
	for k, v := range n.Fields {
		if len(fields) >= 16 {
			break
		}
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "key") || strings.Contains(lk, "authorization") {
			continue
		}
		fields[m.clean(k, 64)] = m.clean(v, 512)
	}
	n.Fields = fields
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	select {
	case m.notices <- n:
		return true
	default:
		m.st.AlertsDropped++
		return false
	}
}
func (m *Manager) Log(s Server, kind, line string) bool {
	if !m.cfg.Logs.Enabled || !idPattern.MatchString(s.UUID) {
		return false
	}
	item := logItem{s: Server{UUID: s.UUID}, kind: strings.ReplaceAll(m.clean(kind, 32), "\n", " "), line: strings.ReplaceAll(m.clean(line, MaxLineLen), "\n", " "), at: m.opts.Now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	select {
	case m.logs <- item:
		return true
	default:
		m.st.LogsDropped++
		return false
	}
}
func (m *Manager) Crash(s Server, r CrashReport) bool {
	if !m.cfg.Crashes.Enabled || !idPattern.MatchString(s.UUID) {
		return false
	}
	now := m.opts.Now()
	if r.Time.IsZero() {
		r.Time = now
	}
	s = m.server(s)
	if len(r.Lines) > MaxCrashLines {
		r.Lines = r.Lines[len(r.Lines)-MaxCrashLines:]
	}
	lines := make([]string, len(r.Lines))
	for i, l := range r.Lines {
		lines[i] = strings.ReplaceAll(m.clean(l, MaxLineLen), "\n", " ")
	}
	r.Lines = lines
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	if last, ok := m.lastCrash[s.UUID]; ok && now.Sub(last) < time.Minute {
		m.st.CrashesRateLimited++
		return false
	}
	for id, last := range m.lastCrash {
		if now.Sub(last) >= time.Minute {
			delete(m.lastCrash, id)
		}
	}
	if len(m.lastCrash) >= 65536 {
		m.st.CrashesDropped++
		return false
	}
	select {
	case m.crashes <- crashItem{s, r}:
		m.lastCrash[s.UUID] = now
		return true
	default:
		m.st.CrashesDropped++
		return false
	}
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	s.AlertsQueued = len(m.notices)
	s.LogsQueued = len(m.logs)
	s.CrashesQueued = len(m.crashes)
	return s
}

// Close stops accepting inputs. Cancel Run's context to stop workers and await
// Run before releasing the manager. Queued items are discarded on cancellation.
func (m *Manager) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true; return nil }
func (m *Manager) record(e error, f func(*Status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(&m.st)
	if e != nil {
		m.st.LastError = m.clean(e.Error(), 512)
	}
}
func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	if m.running || m.closed {
		m.mu.Unlock()
		return errors.New("observability manager already run or closed")
	}
	m.running = true
	m.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); m.runAlerts(ctx) }()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case l := <-m.logs:
				if ctx.Err() != nil {
					return
				}
				e := m.writeLog(ctx, l)
				m.record(e, func(s *Status) {
					if e == nil {
						s.LogsWritten++
					} else {
						s.LogsFailed++
					}
				})
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-m.crashes:
				if ctx.Err() != nil {
					return
				}
				_, e := m.capture(ctx, c.s, c.r)
				m.record(e, func(s *Status) {
					if e == nil {
						s.CrashesWritten++
					} else {
						s.CrashesFailed++
					}
				})
			}
		}
	}()
	<-ctx.Done()
	m.Close()
	wg.Wait()
	m.mu.Lock()
	m.st.AlertsDropped += uint64(len(m.notices))
	m.st.LogsDropped += uint64(len(m.logs))
	m.st.CrashesDropped += uint64(len(m.crashes))
	for len(m.notices) > 0 {
		<-m.notices
	}
	for len(m.logs) > 0 {
		<-m.logs
	}
	for len(m.crashes) > 0 {
		<-m.crashes
	}
	m.mu.Unlock()
	return ctx.Err()
}
