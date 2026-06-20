package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"viberouter/internal/model"
)

// FileLogger writes call logs as JSON Lines asynchronously and supports
// querying them back for the web UI.
type FileLogger struct {
	filePath string
	logChan  chan *model.CallLog
	wg       sync.WaitGroup
}

// jsonLine is one row on disk: a timestamp plus the call record fields.
type jsonLine struct {
	Timestamp string `json:"timestamp"`
	model.CallLog
}

var (
	fileLogger     *FileLogger
	fileLoggerOnce sync.Once
)

// InitFileLogger initializes the file logger singleton.
func InitFileLogger(filePath string) error {
	var initErr error
	fileLoggerOnce.Do(func() {
		dir := filePath
		// Treat a directory path as "<dir>/viberouter.jsonl" for back-compat.
		if strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, "\\") || !strings.Contains(base(dir), ".") {
			dir = joinPath(dir, "viberouter.jsonl")
		}
		if err := os.MkdirAll(parent(dir), 0755); err != nil {
			initErr = fmt.Errorf("failed to create log directory: %w", err)
			return
		}
		fileLogger = &FileLogger{
			filePath: dir,
			logChan:  make(chan *model.CallLog, 1000),
		}
		fileLogger.wg.Add(1)
		go fileLogger.writeLoop()
		log.Printf("[FileLogger] Initialized at %s", dir)
	})
	return initErr
}

func GetFileLogger() *FileLogger { return fileLogger }

// Log queues an entry (async, non-blocking).
func (f *FileLogger) Log(entry *model.CallLog) {
	if f == nil || f.logChan == nil {
		return
	}
	select {
	case f.logChan <- entry:
	default:
		log.Printf("[FileLogger] WARNING: log channel full, dropping entry")
	}
}

func (f *FileLogger) writeLoop() {
	defer f.wg.Done()
	for entry := range f.logChan {
		f.writeEntry(entry)
	}
}

func (f *FileLogger) writeEntry(entry *model.CallLog) {
	if f == nil {
		return
	}
	row := jsonLine{Timestamp: time.Now().Format("2006-01-02 15:04:05"), CallLog: *entry}

	// Colored console output.
	consoleOutput(f.entryToConsoleString(&row))

	// JSON Lines file output.
	data, err := json.Marshal(row)
	if err != nil {
		log.Printf("[FileLogger] ERROR: marshal entry: %v", err)
		return
	}
	file, err := os.OpenFile(f.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[FileLogger] ERROR: open log file: %v", err)
		return
	}
	file.Write(append(data, '\n'))
	file.Close()
}

// LogFilter selects log rows for the web UI. Empty fields match all.
type LogFilter struct {
	Username string
	Model    string
	Tier     string
	ApiStyle string
	Status   string // "2xx" / "4xx" / "5xx" / exact code
	Limit    int
}

// Query reads the JSON Lines log and returns matching rows, newest first.
func (f *FileLogger) Query(filter LogFilter) []jsonLine {
	if f == nil {
		return nil
	}
	file, err := os.Open(f.filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	limit := filter.Limit
	if limit <= 0 {
		limit = 500
	}

	var matched []jsonLine
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row jsonLine
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if !rowMatches(&row, filter) {
			continue
		}
		matched = append(matched, row)
	}
	if len(matched) > limit {
		matched = matched[len(matched)-limit:]
	}
	// Newest first.
	for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}
	return matched
}

func rowMatches(row *jsonLine, f LogFilter) bool {
	if f.Username != "" && !strings.EqualFold(row.Username, f.Username) {
		return false
	}
	if f.Model != "" && !strings.Contains(strings.ToLower(row.ModelName), strings.ToLower(f.Model)) &&
		!strings.Contains(strings.ToLower(row.ModelDisplayName), strings.ToLower(f.Model)) {
		return false
	}
	if f.Tier != "" && !strings.EqualFold(string(row.Tier), f.Tier) {
		return false
	}
	if f.ApiStyle != "" && !strings.EqualFold(row.ApiStyle, f.ApiStyle) {
		return false
	}
	if f.Status != "" {
		switch f.Status {
		case "2xx":
			if row.StatusCode < 200 || row.StatusCode >= 300 {
				return false
			}
		case "4xx":
			if row.StatusCode < 400 || row.StatusCode >= 500 {
				return false
			}
		case "5xx":
			if row.StatusCode < 500 || row.StatusCode > 599 {
				return false
			}
		default:
			if fmt.Sprintf("%d", row.StatusCode) != f.Status {
				return false
			}
		}
	}
	return true
}

// Close drains the writer.
func (f *FileLogger) Close() {
	if f == nil || f.logChan == nil {
		return
	}
	close(f.logChan)
	f.wg.Wait()
}

// Console color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorMagenta = "\033[35m"
)

func consoleOutput(msg string) {
	fmt.Println(msg)
}

func (f *FileLogger) entryToConsoleString(entry *jsonLine) string {
	var statusColor, statusStr string
	switch {
	case entry.StatusCode >= 500:
		statusColor = colorRed
	case entry.StatusCode >= 400:
		statusColor = colorYellow
	default:
		statusColor = colorGreen
	}
	statusStr = fmt.Sprintf("%s%d%s", statusColor, entry.StatusCode, colorReset)

	var providerColor string
	switch entry.Provider {
	case "openai":
		providerColor = colorGreen
	case "anthropic":
		providerColor = colorYellow
	default:
		providerColor = colorGray
	}
	providerStr := fmt.Sprintf("%s%s%s", providerColor, entry.Provider, colorReset)
	usernameStr := fmt.Sprintf("%s%s%s", colorCyan, entry.Username, colorReset)
	modelStr := fmt.Sprintf("%s%s%s", colorBlue, entry.ModelName, colorReset)

	tierStr := ""
	if entry.Tier != "" {
		tc := colorMagenta
		if entry.Tier == model.TierBasic {
			tc = colorGray
		}
		tierStr = fmt.Sprintf(" %s[%s]%s", tc, entry.Tier, colorReset)
	}
	if entry.IsLongContext {
		tierStr += fmt.Sprintf(" %sLONG%s", colorMagenta, colorReset)
	}

	var latencyColor string
	switch {
	case entry.LatencyMs < 1000:
		latencyColor = colorGreen
	case entry.LatencyMs < 5000:
		latencyColor = colorYellow
	default:
		latencyColor = colorRed
	}
	latencyStr := fmt.Sprintf("%s%dms%s", latencyColor, entry.LatencyMs, colorReset)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s[%s]%s ", colorGray, entry.Timestamp, colorReset))
	sb.WriteString(fmt.Sprintf("| %s %s ", usernameStr, providerStr))
	sb.WriteString(fmt.Sprintf("| %s %s", modelStr, entry.ModelDisplayName))
	sb.WriteString(tierStr)
	sb.WriteString(fmt.Sprintf(" %s %s %s ", entry.RequestMethod, entry.RequestPath, statusStr))
	sb.WriteString(latencyStr)
	if entry.ErrorMessage != "" {
		sb.WriteString(fmt.Sprintf(" %sERROR: %s%s", colorRed, entry.ErrorMessage, colorReset))
	}
	return sb.String()
}

// --- minimal path helpers (avoid importing filepath edge cases on Windows) ---

func base(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func parent(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func joinPath(dir, file string) string {
	dir = strings.ReplaceAll(dir, "\\", "/")
	dir = strings.TrimSuffix(dir, "/")
	return dir + "/" + file
}
