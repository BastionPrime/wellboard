// Phase 7 tests for the listen-address resolution (NFR-2.1 LAN-only).

package main

import (
	"net"
	"testing"
)

func setBind(t *testing.T, v string) {
	t.Helper()
	old := *bindFlag
	*bindFlag = v
	t.Cleanup(func() { *bindFlag = old })
}

// With no --bind, prod resolves the br-lan address when present and
// loopback otherwise; dev is always loopback.
func TestListenHostDefaults(t *testing.T) {
	// Dev: loopback regardless of br-lan.
	if got := listenHost(true, false); got != "127.0.0.1" {
		t.Fatalf("dev listen = %q, want 127.0.0.1", got)
	}

	// Prod on a host WITHOUT br-lan: fallback loopback (logged).
	if ipOfInterface("br-lan") == "" {
		if got := listenHost(false, false); got != "127.0.0.1" {
			t.Fatalf("prod (no br-lan) listen = %q, want 127.0.0.1 fallback", got)
		}
	} else {
		ip := ipOfInterface("br-lan")
		if got := listenHost(false, false); got != ip {
			t.Fatalf("prod listen = %q, want br-lan ip %q", got, ip)
		}
	}
}

// --bind with an explicit IP wins; --bind "" explicitly opts into all
// interfaces; --bind <iface> resolves the interface IP.
func TestListenHostBindFlag(t *testing.T) {
	setBind(t, "192.168.7.1")
	if got := listenHost(false, false); got != "192.168.7.1" {
		t.Fatalf("bind ip: listen = %q", got)
	}
	setBind(t, "10.1.2.3")
	if got := listenHost(true, false); got != "10.1.2.3" {
		t.Fatalf("dev+bind: listen = %q", got)
	}

	// Explicit empty --bind: all interfaces (":port" form upstream).
	setBind(t, "")
	if got := listenHost(false, true); got != "" {
		t.Fatalf("bind empty: listen = %q, want \"\"", got)
	}
	// Empty but NOT set: default path (dev loopback).
	if got := listenHost(true, false); got != "127.0.0.1" {
		t.Fatalf("no bind, dev: listen = %q, want 127.0.0.1", got)
	}

	// Interface name resolution via loopback (present everywhere).
	setBind(t, "lo")
	if ip := ipOfInterface("lo"); ip != "" {
		if got := listenHost(false, false); got != ip {
			t.Fatalf("bind lo: listen = %q, want %q", got, ip)
		}
	} else {
		t.Skip("no lo interface")
	}
}

// ipOfInterface: nonexistent interface → "".
func TestIPOfInterfaceMissing(t *testing.T) {
	if got := ipOfInterface("no-such-iface-xyz"); got != "" {
		t.Fatalf("missing iface: %q", got)
	}
}

// addr form: JoinHostPort produces "host:port" ("" → ":port").
func TestAddrForm(t *testing.T) {
	setBind(t, "")
	if got := net.JoinHostPort(listenHost(false, true), "8090"); got != ":8090" {
		t.Fatalf("addr = %q, want :8090", got)
	}
	setBind(t, "192.168.1.1")
	if got := net.JoinHostPort(listenHost(false, false), "8090"); got != "192.168.1.1:8090" {
		t.Fatalf("addr = %q", got)
	}
}
