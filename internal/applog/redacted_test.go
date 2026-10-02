// Secret gate: the RedactedLogger helper itself. The gate scenarios
// (subscription URL update, sensitive import field, end-to-end applog)
// live in internal/api and internal/subscription; this file pins the
// helper's contract, because both of those packages lean on it.
package applog

import (
	"testing"
)

func TestRedactedLoggerLogCallback(t *testing.T) {
	r := &RedactedLogger{}
	var logFn func(format string, args ...any) = r.Log // the SetLog/Updater.Log shape
	logFn("source %s: fetch failed: %s", "sub_1", "dial timeout")
	logFn("state imported: %d sources", 2)

	got := r.Lines()
	if len(got) != 2 || got[0] != "source sub_1: fetch failed: dial timeout" {
		t.Fatalf("callback capture wrong: %q", got)
	}
}

func TestRedactedLoggerAsWriter(t *testing.T) {
	r := &RedactedLogger{}
	// The io.Writer path the daemon's log.Printf output takes.
	if _, err := r.Write([]byte("apply: profile p1 ok=true\n")); err != nil {
		t.Fatal(err)
	}
	r.Printf("multi %d", 3)
	if got := r.Lines(); len(got) != 2 || got[1] != "multi 3" {
		t.Fatalf("writer capture wrong: %q", got)
	}
}

func TestRedactedLoggerHasSecretFailsOnLeak(t *testing.T) {
	r := &RedactedLogger{}
	r.Log("source sub_1: fetch https://panel.example/api/v1/clash?token=ZmFrZQ failed")

	// Self-check: the helper MUST detect an actual leak — this is what
	// makes the acceptance criterion "the test fails when a token is
	// added to the log" true.
	if leaks := r.SecretLeaks("ZmFrZQ"); len(leaks) != 1 {
		t.Fatalf("SecretLeaks must find the leaked token, got %q", leaks)
	}

	// And report nothing when the marker is absent.
	r2 := &RedactedLogger{}
	r2.Log("source sub_1: fetch failed: network: [url redacted]")
	if leaks := r2.SecretLeaks("ZmFrZQ"); len(leaks) != 0 {
		t.Fatalf("SecretLeaks false positive: %q", leaks)
	}
}

func TestRedactedLoggerHostOnlyShape(t *testing.T) {
	// Host + no URL prefix: the accepted redaction shape.
	good := &RedactedLogger{}
	good.Log("source sub_1: fetch failed: dial panel.example:443: timeout")
	if leaks := good.SecretLeaks("http://"); len(leaks) != 0 {
		t.Fatalf("host-only line must not leak URL shape: %q", leaks)
	}

	// A token-less URL still fails HostOnly: URL-shaped fragments in
	// logs are one edit away from carrying the token again.
	bad := &RedactedLogger{}
	bad.Log("source sub_1: fetch https://panel.example/api failed")
	if leaks := bad.SecretLeaks("https://"); len(leaks) != 1 {
		t.Fatalf("SecretLeaks must flag URL-shaped lines, got %q", leaks)
	}

	// *** masking is accepted as an alternative to the host.
	masked := &RedactedLogger{}
	masked.Log("source sub_1: url ***")
	masked.HasSecret(t, "ZmFrZQ")
	masked.HostOnly(t, "panel.example")
}
