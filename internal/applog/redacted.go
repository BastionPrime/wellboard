// Test support for the "no secrets in logs" regression gate (audit §5).
// RedactedLogger records every formatted log line and offers the
// assertions used by the gate tests. It is deliberately dependency-free
// (fmt, strings, sync, testing), so any package can import it.
//
// Terminology used by the assertions:
//   - a "secret marker" is a short random-looking token embedded in the
//     line by the test (the stand-in for a credential: subscription
//     token, proxy password, HWID);
//   - "host only" means the log line may name the host but nothing
//     after it (path, query, userinfo) of the secret-carrying URL.
package applog

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// RedactedLogger is a log capture sink: wire it wherever the codebase
// accepts a func(format string, args ...any) logger (api.Server.SetLog,
// subscription.Updater.Log) or use it directly as an applog writer.
type RedactedLogger struct {
	mu    sync.Mutex
	lines []string
}

// Log implements the func(format string, args ...any) callback shape.
func (r *RedactedLogger) Log(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

// Write implements io.Writer, so *RedactedLogger can serve as the
// applog sink (the same path the daemon's log.Printf output takes).
func (r *RedactedLogger) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Printf implements the applog printf sink shape.
func (r *RedactedLogger) Printf(format string, args ...any) {
	r.Log(format, args...)
}

// Lines returns a copy of the captured lines so far.
func (r *RedactedLogger) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// String joins the captured lines with newlines.
func (r *RedactedLogger) String() string {
	return strings.Join(r.Lines(), "\n")
}

// SecretLeaks returns every captured line that contains the marker
// (the stand-in secret). Pure: callers assert on the result.
func (r *RedactedLogger) SecretLeaks(marker string) []string {
	if marker == "" {
		panic("SecretLeaks: empty marker — the test would assert nothing")
	}
	var leaks []string
	for _, line := range r.Lines() {
		if strings.Contains(line, marker) {
			leaks = append(leaks, line)
		}
	}
	return leaks
}

// HasSecret asserts that no captured line contains the marker: it
// fails the test naming the leaked line when the marker shows up.
func (r *RedactedLogger) HasSecret(t *testing.T, marker string) {
	t.Helper()
	if leaks := r.SecretLeaks(marker); len(leaks) > 0 {
		t.Fatalf("secret %q leaked into log line: %q", marker, leaks[0])
	}
}

// HostOnly asserts the shape of an acceptably-redacted trace: the
// capture DOES mention host (or a *** mask) so operators can still see
// which subscription acted, but it never contains a URL prefix —
// a line like "https://host/[url redacted]" still fails, because any
// URL-shaped fragment in a log line is one edit away from carrying the
// token again.
func (r *RedactedLogger) HostOnly(t *testing.T, host string) {
	t.Helper()
	if host == "" {
		t.Fatal("HostOnly: empty host — the test would assert nothing")
	}
	all := r.String()
	if !strings.Contains(all, host) && !strings.Contains(all, "***") {
		t.Fatalf("expected host %q (or ***) in logs, got: %q", host, all)
	}
	r.HasSecret(t, "http://")
	r.HasSecret(t, "https://")
}
