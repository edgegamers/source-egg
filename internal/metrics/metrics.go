// Package metrics contains read-only resource sampling and presentation.
package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled       bool   `json:"enabled"`
	Listen        string `json:"listen"`
	SampleSeconds int    `json:"sample_seconds"`
}

func DefaultConfig() Config { return Config{Listen: "127.0.0.1:9151", SampleSeconds: 1} }
func (c Config) Validate() error {
	if _, _, e := net.SplitHostPort(c.Listen); e != nil {
		return fmt.Errorf("metrics listen: %w", e)
	}
	if c.SampleSeconds < 1 || c.SampleSeconds > 60 {
		return fmt.Errorf("metrics sample_seconds must be 1–60")
	}
	return nil
}

type CPU struct {
	Usage struct {
		Total  uint64   `json:"total_usage"`
		PerCPU []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	System uint64 `json:"system_cpu_usage"`
	Online int    `json:"online_cpus"`
}
type Network struct {
	RX uint64 `json:"rx_bytes"`
	TX uint64 `json:"tx_bytes"`
}
type DockerStats struct {
	CPU      CPU `json:"cpu_stats"`
	Previous CPU `json:"precpu_stats"`
	Memory   struct {
		Usage, Limit uint64
		Stats        map[string]uint64
	} `json:"memory_stats"`
	PIDs     struct{ Current uint64 } `json:"pids_stats"`
	Networks map[string]Network       `json:"networks"`
}
type Sample struct {
	CPU    float64 `json:"cpu_percent"`
	Memory uint64  `json:"memory_bytes"`
	Limit  uint64  `json:"memory_limit_bytes"`
	PIDs   uint64  `json:"pids"`
	RX     uint64  `json:"rx_bytes"`
	TX     uint64  `json:"tx_bytes"`
	RXRate float64 `json:"rx_per_second"`
	TXRate float64 `json:"tx_per_second"`
}
type Server struct {
	ID, Name, Game, Map string
	Running, Connected  bool
	Players             int
	Sample              Sample
	Error               string `json:"error,omitempty"`
}
type Host struct {
	CPU           float64
	Cores         int
	Memory, Limit uint64
}
type Snapshot struct {
	Version, Channel string
	Uptime           float64
	At               time.Time
	Host             Host
	Servers          []Server
}

func Measure(now DockerStats, old *DockerStats, elapsed time.Duration) Sample {
	out := Sample{Memory: now.Memory.Usage, Limit: now.Memory.Limit, PIDs: now.PIDs.Current}
	cache, ok := now.Memory.Stats["inactive_file"]
	if !ok {
		cache = now.Memory.Stats["cache"]
	}
	if cache < out.Memory {
		out.Memory -= cache
	}
	for _, n := range now.Networks {
		out.RX += n.RX
		out.TX += n.TX
	}
	prev := now.Previous
	if old != nil {
		prev = old.CPU
	}
	cpus := now.CPU.Online
	if cpus == 0 {
		cpus = len(now.CPU.Usage.PerCPU)
	}
	if now.CPU.System > prev.System && now.CPU.Usage.Total >= prev.Usage.Total && cpus > 0 {
		out.CPU = float64(now.CPU.Usage.Total-prev.Usage.Total) / float64(now.CPU.System-prev.System) * float64(cpus) * 100
	}
	if old != nil && elapsed > 0 {
		var rx, tx uint64
		for _, n := range old.Networks {
			rx += n.RX
			tx += n.TX
		}
		if out.RX >= rx {
			out.RXRate = float64(out.RX-rx) / elapsed.Seconds()
		}
		if out.TX >= tx {
			out.TXRate = float64(out.TX-tx) / elapsed.Seconds()
		}
	}
	return out
}

type HostCounter struct{ Total, Idle uint64 }

func ReadHost(previous HostCounter) (Host, HostCounter, error) {
	h := Host{Cores: runtime.NumCPU()}
	var next HostCounter
	data, e := os.ReadFile("/proc/stat")
	if e != nil {
		return h, next, e
	}
	fields := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		if i < 8 {
			next.Total += n
		}
		if i == 3 || i == 4 {
			next.Idle += n
		}
	}
	if next.Total > previous.Total && next.Idle >= previous.Idle {
		total, idle := next.Total-previous.Total, next.Idle-previous.Idle
		if idle <= total {
			h.CPU = float64(total-idle) / float64(total) * 100
		}
	}
	mem, e := os.Open("/proc/meminfo")
	if e != nil {
		return h, next, e
	}
	defer mem.Close()
	var available uint64
	scan := bufio.NewScanner(mem)
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		if f[0] == "MemTotal:" {
			h.Limit = v * 1024
		}
		if f[0] == "MemAvailable:" {
			available = v * 1024
		}
	}
	if h.Limit >= available {
		h.Memory = h.Limit - available
	}
	return h, next, scan.Err()
}
func label(s string) string { return strconv.Quote(s) }
func Handler(get func() Snapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		s := get()
		fmt.Fprintf(w, "source_node_info{version=%s,channel=%s} 1\nsource_node_uptime_seconds %g\nsource_node_servers %d\nsource_node_cpu_percent %g\nsource_node_memory_bytes %d\nsource_node_memory_limit_bytes %d\n", label(s.Version), label(s.Channel), s.Uptime, len(s.Servers), s.Host.CPU, s.Host.Memory, s.Host.Limit)
		for _, v := range s.Servers {
			l := "server=" + label(v.ID) + ",game=" + label(v.Game)
			up, connected := 0, 0
			if v.Running {
				up = 1
			}
			if v.Connected {
				connected = 1
			}
			fmt.Fprintf(w, "source_server_up{%s} %d\nsource_server_egg_connected{%s} %d\nsource_server_players{%s} %d\nsource_server_map_info{%s,map=%s} 1\n", l, up, l, connected, l, v.Players, l, label(v.Map))
			for _, n := range []struct {
				name  string
				value float64
			}{{"cpu_percent", v.Sample.CPU}, {"memory_bytes", float64(v.Sample.Memory)}, {"memory_limit_bytes", float64(v.Sample.Limit)}, {"pids", float64(v.Sample.PIDs)}, {"network_receive_bytes_total", float64(v.Sample.RX)}, {"network_transmit_bytes_total", float64(v.Sample.TX)}} {
				fmt.Fprintf(w, "source_server_%s{%s} %g\n", n.name, l, n.value)
			}
		}
	})
}
func SafeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r >= 128 && r <= 159 {
			return -1
		}
		return r
	}, s)
}
func Render(s Snapshot, order string, offset, width, height int) string {
	servers := append([]Server(nil), s.Servers...)
	sort.SliceStable(servers, func(i, j int) bool {
		a, b := servers[i], servers[j]
		switch order {
		case "players":
			if a.Players != b.Players {
				return a.Players > b.Players
			}
		case "memory":
			if a.Sample.Memory != b.Sample.Memory {
				return a.Sample.Memory > b.Sample.Memory
			}
		case "name":
			return a.Name < b.Name
		default:
			if a.Sample.CPU != b.Sample.CPU {
				return a.Sample.CPU > b.Sample.CPU
			}
		}
		return a.ID < b.ID
	})
	var b strings.Builder
	fmt.Fprintf(&b, "sourcenode %s %s | CPU %.1f%% (%d cores) MEM %.1f/%.1f GiB\n", SafeText(s.Version), SafeText(s.Channel), s.Host.CPU, s.Host.Cores, float64(s.Host.Memory)/(1<<30), float64(s.Host.Limit)/(1<<30))
	fmt.Fprintf(&b, "servers %d | sort %s | q quit  s sort  arrows/PageUp/PageDown scroll\n", len(servers), SafeText(order))
	fmt.Fprintln(&b, "SERVER       GAME   NAME                     MAP               PLAYERS   CPU%    MEMORY    RX/s    TX/s")
	if offset < 0 {
		offset = 0
	}
	if offset > len(servers) {
		offset = len(servers)
	}
	if height < 5 {
		height = 5
	}
	end := min(len(servers), offset+height-4)
	for _, v := range servers[offset:end] {
		players := "-"
		if v.Connected {
			players = strconv.Itoa(v.Players)
		}
		id := SafeText(v.ID)
		if len(id) > 10 {
			id = id[:10]
		}
		line := fmt.Sprintf("%-10s %-6s %-24.24s %-17.17s %7s %6.1f %7.1fM %6.0fK %6.0fK", id, SafeText(v.Game), SafeText(v.Name), SafeText(v.Map), players, v.Sample.CPU, float64(v.Sample.Memory)/(1<<20), v.Sample.RXRate/1024, v.Sample.TXRate/1024)
		if width > 0 && len(line) > width {
			line = line[:width]
		}
		fmt.Fprintln(&b, line)
	}
	return b.String()
}
