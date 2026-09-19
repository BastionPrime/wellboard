// Package applog is the application log buffer for FR-9.2: the last
// N lines of the wellboard daemon log, served by GET /api/v1/logs.
//
// The daemon routes its log.Printf output through Capture (installed
// in cmd/wellboard), which keeps a bounded in-memory ring and mirrors
// to <state>/wellboard.log when configured. API request logs, apply
// flow events and adapter actions all land here.
package applog

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// DefaultBuffer is the in-memory ring capacity (lines).
const DefaultBuffer = 1000

// ServeLines is how many lines GET /api/v1/logs returns (FR-9.2: 500).
const ServeLines = 500

// Log is a bounded line buffer, optionally mirrored to a file.
type Log struct {
	mu     sync.Mutex
	lines  []string
	max    int
	path   string
	file   *os.File
	closed bool
}

// New builds a Log. filePath may be "" (memory only). maxLines <= 0
// falls back to DefaultBuffer.
func New(filePath string, maxLines int) *Log {
	if maxLines <= 0 {
		maxLines = DefaultBuffer
	}
	l := &Log{max: maxLines, path: filePath}
	if filePath != "" {
		// Append mode: restarts keep the previous tail.
		if f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			l.file = f
		}
	}
	return l
}

// Write implements io.Writer: every log line is captured (log.Printf
// newline-terminates). Long lines are truncated at 8 KiB so one
// runaway request cannot blow the buffer.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if l.closed {
		return n, nil
	}
	text := strings.TrimRight(string(p), "\n")
	if len(text) > 8192 {
		text = text[:8192] + "…"
	}
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		l.lines = append(l.lines, line)
		if len(l.lines) > l.max {
			l.lines = l.lines[len(l.lines)-l.max:]
		}
	}
	if l.file != nil {
		_, _ = l.file.WriteString(text + "\n")
	}
	return n, nil
}

// Printf formats and writes one line (adapter log callbacks).
func (l *Log) Printf(format string, args ...any) {
	_, _ = l.Write([]byte(fmt.Sprintf(format, args...)))
}

// Tail returns up to n last lines (oldest first). n <= 0 → ServeLines.
// When the buffer is empty but a mirror file exists, the file tail is
// read instead (survives restarts).
func (l *Log) Tail(n int) []string {
	if n <= 0 {
		n = ServeLines
	}
	l.mu.Lock()
	lines := make([]string, len(l.lines))
	copy(lines, l.lines)
	path := l.path
	l.mu.Unlock()
	if len(lines) == 0 && path != "" {
		if data, err := os.ReadFile(path); err == nil {
			all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(all) > n {
				all = all[len(all)-n:]
			}
			return all
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// Close flushes and closes the mirror file.
func (l *Log) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
