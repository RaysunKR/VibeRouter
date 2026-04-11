package service

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileLogger writes call logs to files asynchronously
type FileLogger struct {
	logDir  string
	logChan chan *FileLogEntry
	wg      sync.WaitGroup
}

type FileLogEntry struct {
	Timestamp     string `json:"timestamp"`
	Username      string `json:"username"`
	ClientIP      string `json:"client_ip"`
	Provider      string `json:"provider"`
	ModelName     string `json:"model_name"`
	DisplayName   string `json:"display_name"`
	ApiStyle      string `json:"api_style"`
	RequestPath   string `json:"request_path"`
	Method        string `json:"method"`
	StatusCode    int    `json:"status_code"`
	ErrorMessage  string `json:"error_message,omitempty"`
	LatencyMs     int    `json:"latency_ms"`
}

var (
	fileLogger     *FileLogger
	fileLoggerOnce sync.Once
)

// InitFileLogger initializes the file logger singleton
func InitFileLogger(logDir string) error {
	var initErr error
	fileLoggerOnce.Do(func() {
		// Create log directory if not exists
		if err := os.MkdirAll(logDir, 0755); err != nil {
			initErr = fmt.Errorf("failed to create log directory: %w", err)
			return
		}

		fileLogger = &FileLogger{
			logDir:  logDir,
			logChan: make(chan *FileLogEntry, 1000), // Buffer 1000 entries
		}

		// Start writer goroutine
		fileLogger.wg.Add(1)
		go fileLogger.writeLoop()

		log.Printf("[FileLogger] Initialized at %s", logDir)
	})
	return initErr
}

// GetFileLogger returns the file logger instance
func GetFileLogger() *FileLogger {
	return fileLogger
}

// Log adds an entry to the log channel (async, non-blocking)
func (f *FileLogger) Log(entry *FileLogEntry) {
	if f == nil || f.logChan == nil {
		return
	}
	entry.Timestamp = time.Now().Format("2006-01-02 15:04:05")

	select {
	case f.logChan <- entry:
		// Successfully queued
	default:
		// Channel full, log is dropped - this is intentional for non-blocking
		log.Printf("[FileLogger] WARNING: log channel full, dropping entry")
	}
}

func (f *FileLogger) writeLoop() {
	defer f.wg.Done()
	for entry := range f.logChan {
		f.writeEntry(entry)
	}
}

func (f *FileLogger) writeEntry(entry *FileLogEntry) {
	if f == nil {
		return
	}

	// Console output with colors for command line readability
	consoleOutput(f.entryToConsoleString(entry))

	// File output (pipe-separated for easy parsing)
	filename := filepath.Join(f.logDir, fmt.Sprintf("api-%s.log", time.Now().Format("2006-01-02")))
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[FileLogger] ERROR: failed to open log file: %v", err)
		return
	}

	// Pipe-separated format for log file
	fileLine := fmt.Sprintf("%s | %s | %s | %s | %s | %s | %s | %s | %d | %dms",
		entry.Timestamp,
		entry.Username,
		entry.ClientIP,
		entry.Provider,
		entry.ModelName,
		entry.DisplayName,
		entry.RequestPath,
		entry.Method,
		entry.StatusCode,
		entry.LatencyMs,
	)

	if entry.ErrorMessage != "" {
		fileLine += fmt.Sprintf(" | ERROR: %s", entry.ErrorMessage)
	}

	fileLine += "\n"
	file.Write([]byte(fileLine))
	file.Close()
}

// Console color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue    = "\033[34m"
	colorCyan    = "\033[36m"
	colorWhite   = "\033[37m"
	colorGray    = "\033[90m"
)

func consoleOutput(msg string) {
	fmt.Println(msg)
}

func (f *FileLogger) entryToConsoleString(entry *FileLogEntry) string {
	// Colorize based on status code
	var statusColor string
	var statusStr string
	if entry.StatusCode >= 500 {
		statusColor = colorRed
		statusStr = fmt.Sprintf("%s%d%s", statusColor, entry.StatusCode, colorReset)
	} else if entry.StatusCode >= 400 {
		statusColor = colorYellow
		statusStr = fmt.Sprintf("%s%d%s", statusColor, entry.StatusCode, colorReset)
	} else {
		statusColor = colorGreen
		statusStr = fmt.Sprintf("%s%d%s", statusColor, entry.StatusCode, colorReset)
	}

	// Colorize provider
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

	// Colorize username
	usernameStr := fmt.Sprintf("%s%s%s", colorCyan, entry.Username, colorReset)

	// Colorize model name
	modelStr := fmt.Sprintf("%s%s%s", colorBlue, entry.ModelName, colorReset)

	// Format latency with color
	var latencyColor string
	if entry.LatencyMs < 1000 {
		latencyColor = colorGreen
	} else if entry.LatencyMs < 5000 {
		latencyColor = colorYellow
	} else {
		latencyColor = colorRed
	}
	latencyStr := fmt.Sprintf("%s%dms%s", latencyColor, entry.LatencyMs, colorReset)

	// Build console output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s[%s]%s ", colorGray, entry.Timestamp, colorReset))
	sb.WriteString(fmt.Sprintf("%s| %s ", usernameStr, providerStr))
	sb.WriteString(fmt.Sprintf("%s| %s ", modelStr, entry.DisplayName))
	sb.WriteString(fmt.Sprintf("%s %s %s ", entry.Method, entry.RequestPath, statusStr))
	sb.WriteString(latencyStr)

	if entry.ErrorMessage != "" {
		sb.WriteString(fmt.Sprintf(" %sERROR: %s%s", colorRed, entry.ErrorMessage, colorReset))
	}

	return sb.String()
}

// Close gracefully shuts down the file logger
func (f *FileLogger) Close() {
	if f == nil || f.logChan == nil {
		return
	}
	close(f.logChan)
	f.wg.Wait()
}
