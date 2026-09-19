// Package lan lists LAN devices from DHCP leases (FR-4.4) and proposes
// static leases via UCI dhcp.
//
// Sources, in order:
//  1. /tmp/dhcp.leases — dnsmasq lease file: one line per lease,
//     "<expiry> <mac> <ip> <hostname> <client-id>".
//  2. ubus call dhcp ipv4leases — odhcpd only (verified: odhcpd
//     src/ubus.c handle_dhcpv4_leases; default OpenWrt runs dnsmasq for
//     IPv4, so this usually returns nothing — the lease file is the
//     primary source).
//
// A static lease is pinned with UCI:
//
//	uci add dhcp host; uci set dhcp.@host[-1].name='<hostname>'
//	uci set dhcp.@host[-1].mac='<mac>'; uci set dhcp.@host[-1].ip='<ip>'
//	uci commit dhcp
//
// mihomo matches devices by IP (SRC-IP-CIDR), never by MAC, hence the
// static-lease offer (initial TZ FR-4.4).
//
// Dev mode: without a real /tmp/dhcp.leases and ubus socket the package
// reads the configured lease file (a fixture) and the static-lease
// backend is a stub that documents the UCI commands (SetStatic returns
// errDevStub); on a router the same call shells out to uci.
package lan

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/wellboard/wellboard/internal/model"
)

// LeaseFile is the dnsmasq lease file path (OpenWrt default).
const LeaseFile = "/tmp/dhcp.leases"

// UbusSocket is the ubus RPC socket path used for the odhcpd fallback.
const UbusSocket = "/var/run/ubus.sock"

// UCI binary (static leases).
const uciBin = "uci"

// ErrDevStub is returned by static-lease operations in dev mode: there
// is no UCI on a dev host. The message documents the equivalent router
// commands (see package comment).
var ErrDevStub = errors.New("lan: static lease not applied in dev mode; on the router this runs: " +
	"uci add dhcp host; uci set dhcp.@host[-1].name=<hostname>; " +
	"uci set dhcp.@host[-1].mac=<mac>; uci set dhcp.@host[-1].ip=<ip>; uci commit dhcp")

// Reader lists devices.
type Reader struct {
	// LeaseFile overrides /tmp/dhcp.leases (dev fixture / tests).
	LeaseFile string
	// UbusSocket overrides /var/run/ubus.sock; empty disables the ubus
	// fallback (dev mode).
	UbusSocket string
	// UbusCmd runs `ubus call dhcp ipv4leases`; injected for tests. When
	// nil the package shells out to "ubus".
	UbusCmd func() ([]byte, error)
	// Now is the clock for lease expiry filtering; nil = time.Now.
	Now func() time.Time
}

// Devices merges DHCP lease sources into the LAN device list, sorted by
// hostname then IP. The dnsmasq lease file is authoritative; ubus
// odhcpd output (usually empty on default OpenWrt) fills gaps.
func (r *Reader) Devices() ([]model.LANDevice, error) {
	leasePath := r.LeaseFile
	if leasePath == "" {
		leasePath = LeaseFile
	}
	byMAC := map[string]model.LANDevice{}

	// 1. dnsmasq lease file.
	if f, err := os.Open(leasePath); err == nil {
		defer f.Close()
		now := r.now()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			dev, ok := parseLeaseLine(sc.Text(), now)
			if ok {
				byMAC[dev.MAC] = dev
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("lan: read leases %s: %w", leasePath, err)
	}

	// 2. ubus odhcpd fallback: fills gaps and enriches known devices
	// with the odhcpd "static" flag (lease pinned in UCI).
	if r.UbusCmd != nil || r.ubusEnabled() {
		if devs := r.ubusDevices(); devs != nil {
			for _, d := range devs {
				if known, ok := byMAC[d.MAC]; ok {
					if d.Static {
						known.Static = true
						byMAC[d.MAC] = known
					}
					continue
				}
				byMAC[d.MAC] = d
			}
		}
	}

	out := make([]model.LANDevice, 0, len(byMAC))
	for _, d := range byMAC {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hostname != out[j].Hostname {
			return out[i].Hostname < out[j].Hostname
		}
		return out[i].IP < out[j].IP
	})
	return out, nil
}

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reader) ubusEnabled() bool {
	sock := r.UbusSocket
	if sock == "" {
		return false // dev mode: no ubus
	}
	_, err := os.Stat(sock)
	return err == nil
}

// parseLeaseLine parses a dnsmasq lease line:
// "<expiry-epoch> <mac> <ip> <hostname> <client-id>" (fields 4/5 may be
// "*"). Expired leases are skipped.
func parseLeaseLine(line string, now time.Time) (model.LANDevice, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return model.LANDevice{}, false
	}
	expiry := fields[0]
	mac := fields[1]
	ip := fields[2]
	hostname := "*"
	if len(fields) >= 4 {
		hostname = fields[3]
	}
	if hostname == "*" {
		hostname = ""
	}
	if _, err := net.ParseMAC(mac); err != nil {
		return model.LANDevice{}, false
	}
	if net.ParseIP(ip) == nil {
		return model.LANDevice{}, false
	}
	if exp, err := parseUint(expiry); err == nil && exp > 0 && exp < now.Unix() {
		return model.LANDevice{}, false // expired
	}
	return model.LANDevice{MAC: mac, IP: ip, Hostname: hostname}, true
}

func parseUint(s string) (int64, error) {
	var n int64
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int64(r-'0')
		if n > 1<<62 {
			return 0, errors.New("overflow")
		}
	}
	return n, nil
}

// ubusDevices runs `ubus call dhcp ipv4leases` and flattens the
// response: {"device": {"<iface>": {"leases": [{"mac": "...",
// "hostname": "...", "address": "...", "flags": ["static", ...]}]}}}
// (odhcpd src/ubus.c handle_dhcpv4_leases). MACs are hex strings
// without separators — normalized to colon form.
func (r *Reader) ubusDevices() []model.LANDevice {
	run := r.UbusCmd
	if run == nil {
		run = func() ([]byte, error) {
			return exec.Command("ubus", "call", "dhcp", "ipv4leases").Output()
		}
	}
	out, err := run()
	if err != nil {
		return nil
	}
	var resp struct {
		Device map[string]struct {
			Leases []struct {
				MAC      string   `json:"mac"`
				Hostname string   `json:"hostname"`
				Address  string   `json:"address"`
				Flags    []string `json:"flags"`
			} `json:"leases"`
		} `json:"device"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil
	}
	var devs []model.LANDevice
	for _, iface := range resp.Device {
		for _, l := range iface.Leases {
			mac := normMAC(l.MAC)
			if mac == "" || net.ParseIP(l.Address) == nil {
				continue
			}
			devs = append(devs, model.LANDevice{
				MAC: mac, IP: l.Address, Hostname: l.Hostname,
				Static: hasFlag(l.Flags, "static"),
			})
		}
	}
	return devs
}

// SetStatic pins dev as a static DHCP lease via UCI (FR-4.4). In dev
// mode (no uci binary on PATH… callers can force DevMode) it returns
// ErrDevStub with the equivalent commands documented.
func SetStatic(dev model.LANDevice, devMode bool) error {
	if _, err := net.ParseMAC(dev.MAC); err != nil {
		return fmt.Errorf("lan: bad mac %q: %w", dev.MAC, err)
	}
	if net.ParseIP(dev.IP) == nil {
		return fmt.Errorf("lan: bad ip %q", dev.IP)
	}
	if devMode {
		return ErrDevStub
	}
	// Router path: UCI dhcp host section.
	cmds := [][]string{
		{"add", "dhcp", "host"},
		{"set", "dhcp.@host[-1].name=" + dev.Hostname},
		{"set", "dhcp.@host[-1].mac=" + dev.MAC},
		{"set", "dhcp.@host[-1].ip=" + dev.IP},
		{"commit", "dhcp"},
	}
	for _, c := range cmds {
		if out, err := exec.Command(uciBin, c...).CombinedOutput(); err != nil {
			return fmt.Errorf("lan: uci %v: %w: %s", c, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// normMAC converts odhcpd's bare-hex MAC ("aabbccddeeff") to colon
// form; already-separated MACs pass through.
func normMAC(s string) string {
	if s == "" {
		return ""
	}
	if strings.Contains(s, ":") {
		if _, err := net.ParseMAC(s); err == nil {
			return s
		}
		return ""
	}
	if len(s) != 12 {
		return ""
	}
	var b []byte
	for i := 0; i < 12; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			b = append(b, c)
		default:
			return ""
		}
	}
	var sb strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteByte(b[i])
		sb.WriteByte(b[i+1])
	}
	if _, err := net.ParseMAC(sb.String()); err != nil {
		return ""
	}
	return sb.String()
}
