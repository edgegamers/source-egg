package observability

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type LogEntry struct {
	UUID       string    `json:"uuid"`
	Name       string    `json:"name"`
	Date       time.Time `json:"date"`
	Bytes      int64     `json:"bytes"`
	Compressed bool      `json:"compressed"`
}

func (m *Manager) writeLog(ctx context.Context, l logItem) error {
	m.disk.Lock()
	defer m.disk.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	root, e := privateRoot(m.cfg.Logs.Dir)
	if e != nil {
		return e
	}
	defer root.Close()
	r, e := subRoot(root, l.s.UUID, true)
	if e != nil {
		return e
	}
	defer r.Close()
	name := l.at.UTC().Format("2006-01-02") + ".log"
	if st, e := r.Lstat(name); e == nil && !st.Mode().IsRegular() {
		return errors.New("log target is not a regular file")
	}
	f, e := r.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY|noFollowFlags, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("log target is not regular")
	}
	line := fmt.Sprintf("%s [%s] %s\n", l.at.UTC().Format(time.RFC3339), l.kind, l.line)
	if st.Size()+int64(len(line)) > m.cfg.Logs.MaxDailyBytes {
		return errors.New("daily log byte limit reached")
	}
	_, e = io.WriteString(f, line)
	return e
}
func logDate(name string) (time.Time, bool) {
	s := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".log")
	if name != s+".log" && name != s+".log.gz" {
		return time.Time{}, false
	}
	t, e := time.Parse("2006-01-02", s)
	return t, e == nil
}
func (m *Manager) ListLogs(ctx context.Context, uuid string) ([]LogEntry, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !m.cfg.Logs.Enabled {
		return nil, nil
	}
	m.disk.Lock()
	defer m.disk.Unlock()
	root, e := privateRoot(m.cfg.Logs.Dir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	out := []LogEntry{}
	e = serverRoots(root, uuid, func(id string, r *os.Root) error {
		entries, e := readDir(r)
		if e != nil {
			return e
		}
		for _, d := range entries {
			if e := ctx.Err(); e != nil {
				return e
			}
			date, ok := logDate(d.Name())
			if !ok || !d.Type().IsRegular() {
				continue
			}
			f, e := regularOpen(r, d.Name())
			if e != nil {
				continue
			}
			st, e := f.Stat()
			f.Close()
			if e != nil {
				continue
			}
			out = append(out, LogEntry{UUID: id, Name: d.Name(), Date: date, Bytes: st.Size(), Compressed: strings.HasSuffix(d.Name(), ".gz")})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].UUID < out[j].UUID
		}
		return out[i].Date.After(out[j].Date)
	})
	return out, e
}

// Sweep compresses closed UTC days and applies retention for all server folders,
// including servers that no longer run. The daemon owns the sweep schedule.
func (m *Manager) Sweep(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	m.disk.Lock()
	defer m.disk.Unlock()
	now := m.opts.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if m.cfg.Logs.Enabled {
		root, e := privateRoot(m.cfg.Logs.Dir)
		if e != nil {
			return e
		}
		e = serverRoots(root, "", func(_ string, r *os.Root) error {
			entries, e := readDir(r)
			if e != nil {
				return e
			}
			keep := m.cfg.Logs.KeepDays
			if keep == 0 || keep > HardLogDays {
				keep = HardLogDays
			}
			cutoff := day.AddDate(0, 0, -keep)
			for _, d := range entries {
				if e := ctx.Err(); e != nil {
					return e
				}
				date, ok := logDate(d.Name())
				if !ok || !d.Type().IsRegular() {
					continue
				}
				if date.Before(cutoff) {
					if e = r.Remove(d.Name()); e != nil && !os.IsNotExist(e) {
						return e
					}
					continue
				}
				if date.Before(day) && !strings.HasSuffix(d.Name(), ".gz") {
					if e = compressLog(ctx, r, d.Name()); e != nil {
						return e
					}
				}
			}
			return nil
		})
		root.Close()
		if e != nil {
			return e
		}
	}
	if m.cfg.Crashes.Enabled {
		return m.pruneCrashes(ctx, now)
	}
	return nil
}
func compressLog(ctx context.Context, r *os.Root, name string) error {
	f, e := regularOpen(r, name)
	if e != nil {
		return e
	}
	defer f.Close()
	tmp := name + ".gz.tmp"
	// A crashed daemon may leave an incomplete temporary gzip. Removing its
	// directory entry through the root never follows a replacement symlink.
	if e = r.Remove(tmp); e != nil && !os.IsNotExist(e) {
		return e
	}
	out, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollowFlags, 0600)
	if e != nil {
		return e
	}
	defer r.Remove(tmp)
	defer out.Close()
	// Late queued lines may recreate the raw file after its UTC day was
	// compressed. Preserve the existing gzip stream and append another member;
	// standard gzip readers concatenate their decompressed contents.
	if previous, e := regularOpen(r, name+".gz"); e == nil {
		_, copyErr := io.Copy(out, contextReader{ctx, previous})
		previous.Close()
		if copyErr != nil {
			return copyErr
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	gz := gzip.NewWriter(out)
	_, e = io.Copy(gz, contextReader{ctx, f})
	if closeErr := gz.Close(); e == nil {
		e = closeErr
	}
	if syncErr := out.Sync(); e == nil {
		e = syncErr
	}
	if closeErr := out.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = r.Rename(tmp, name+".gz"); e != nil {
		return e
	}
	return r.Remove(name)
}
