// Package logger appends request logs as JSON Lines to daily-rotated files
// and serves filtered queries to the admin UI. Files older than the retention
// period (default 7 days) are deleted automatically.
package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultRetentionDays is how long daily log files are kept.
const DefaultRetentionDays = 7

type Entry struct {
	Ts           string `json:"ts"`
	KeyName      string `json:"key_name,omitempty"`
	ClientProto  string `json:"client_proto"`
	RequestModel string `json:"request_model"`
	Mode         string `json:"mode,omitempty"` // direct | balance | smart
	Group        string `json:"group,omitempty"`
	Tier         string `json:"tier,omitempty"`
	Forced       bool   `json:"forced,omitempty"`
	RouteModel   string `json:"route_model,omitempty"` // resolved router-facing model
	Upstream     string `json:"upstream_model,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Stream       bool   `json:"stream"`
	Status       int    `json:"status"`
	LatencyMs    int64  `json:"latency_ms"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	Attempts     int    `json:"attempts,omitempty"`
	Error        string `json:"error,omitempty"`
	DispErr      string `json:"dispatcher_error,omitempty"`
}

// FileLogger writes to <base>-YYYY-MM-DD.<ext>, rotating at local midnight and
// purging files older than the retention window.
type FileLogger struct {
	basePath      string // e.g. logs/router.log
	retentionDays int

	mu      sync.Mutex
	curDate string
	f       *os.File
	stopCh  chan struct{}
}

// Open creates the logger. retentionDays <= 0 uses DefaultRetentionDays.
func Open(basePath string, retentionDays int) (*FileLogger, error) {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return nil, err
	}
	l := &FileLogger{basePath: basePath, retentionDays: retentionDays, stopCh: make(chan struct{})}
	if err := l.rotateLocked(time.Now()); err != nil {
		return nil, err
	}
	l.purgeOld()
	// background daily purge (rotation itself happens lazily on write)
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				l.purgeOld()
			case <-l.stopCh:
				return
			}
		}
	}()
	return l, nil
}

// dailyPath maps base logs/router.log -> logs/router-2006-01-02.log.
func (l *FileLogger) dailyPath(t time.Time) string {
	dir := filepath.Dir(l.basePath)
	base := filepath.Base(l.basePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, name+"-"+t.Format("2006-01-02")+ext)
}

func (l *FileLogger) rotateLocked(now time.Time) error {
	today := now.Format("2006-01-02")
	if l.curDate == today && l.f != nil {
		return nil
	}
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	f, err := os.OpenFile(l.dailyPath(now), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	l.f = f
	l.curDate = today
	return nil
}

func (l *FileLogger) Log(e Entry) {
	if e.Ts == "" {
		e.Ts = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.rotateLocked(time.Now()); err != nil {
		return
	}
	_, _ = l.f.Write(append(data, '\n'))
}

// purgeOld deletes daily files older than the retention window.
func (l *FileLogger) purgeOld() {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := filepath.Dir(l.basePath)
	base := filepath.Base(l.basePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	matches, err := filepath.Glob(filepath.Join(dir, name+"-????-??-??"+ext))
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)
	for _, m := range matches {
		dateStr := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), name+"-"), ext)
		t, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) && m != l.dailyPath(time.Now()) {
			_ = os.Remove(m)
		}
	}
}

// SetRetentionDays updates the retention window at runtime (admin settings).
func (l *FileLogger) SetRetentionDays(days int) {
	if days <= 0 {
		days = DefaultRetentionDays
	}
	l.mu.Lock()
	l.retentionDays = days
	l.mu.Unlock()
	l.purgeOld()
}

// SetBasePath switches future writes and queries to a new daily log family.
func (l *FileLogger) SetBasePath(basePath string) error {
	if basePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return err
	}
	now := time.Now()
	dailyPath := (&FileLogger{basePath: basePath}).dailyPath(now)
	f, err := os.OpenFile(dailyPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	l.mu.Lock()
	if l.basePath == basePath {
		l.mu.Unlock()
		return f.Close()
	}
	old := l.f
	l.basePath = basePath
	l.f = f
	l.curDate = now.Format("2006-01-02")
	l.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	l.purgeOld()
	return nil
}

func (l *FileLogger) Close() error {
	close(l.stopCh)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		return l.f.Close()
	}
	return nil
}

// QueryFilter narrows log queries. All fields optional.
type QueryFilter struct {
	Limit  int
	Status int    // exact status match, 0 = any
	Model  string // substring match against request/route model
	Key    string // substring match against key name
	Since  string // RFC3339
}

// Query scans daily files within the retention window (newest first) and
// returns the most recent matching entries, newest first.
func (l *FileLogger) Query(q QueryFilter) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 200
	}
	var since time.Time
	if q.Since != "" {
		since, _ = time.Parse(time.RFC3339, q.Since)
	}

	var out []Entry
	now := time.Now()
	for i := 0; i < l.retentionDays && len(out) < q.Limit; i++ {
		entries, err := l.readDay(now.AddDate(0, 0, -i), q, since)
		if err != nil {
			return nil, err
		}
		// entries from a newer day precede older days
		out = append(entries, out...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ts > out[j].Ts })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// readDay reads one daily file (missing file = no entries), newest first.
func (l *FileLogger) readDay(day time.Time, q QueryFilter, since time.Time) ([]Entry, error) {
	f, err := os.Open(l.dailyPath(day))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if q.Status != 0 && e.Status != q.Status {
			continue
		}
		if q.Model != "" && !strings.Contains(e.RequestModel, q.Model) && !strings.Contains(e.RouteModel, q.Model) {
			continue
		}
		if q.Key != "" && !strings.Contains(e.KeyName, q.Key) {
			continue
		}
		if !since.IsZero() {
			t, err := time.Parse(time.RFC3339, e.Ts)
			if err != nil || t.Before(since) {
				continue
			}
		}
		out = append(out, e)
	}
	return out, nil
}
