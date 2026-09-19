package lan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/model"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseLeaseFile(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	// Expiry far in the future (dnsmasq writes epoch seconds).
	future := time.Now().Add(24 * time.Hour).Unix()
	writeFile(t, leases, fmt.Sprintf("%d aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n", future)+
		fmt.Sprintf("%d 11:22:33:44:55:66 192.168.1.51 laptop 01:xx\n", future)+
		fmt.Sprintf("%d deadbeef 192.168.1.52 badmac *\n", future)+ // bad MAC: skipped
		fmt.Sprintf("%d aa:00:00:00:00:01 notanip *\n", future)+ // bad IP: skipped
		"\n"+
		"garbage\n")
	r := &Reader{LeaseFile: leases}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("want 2 devices, got %d: %+v", len(devs), devs)
	}
	// Sorted by hostname: laptop < tv.
	if devs[0].Hostname != "laptop" || devs[0].IP != "192.168.1.51" {
		t.Fatalf("unexpected first: %+v", devs[0])
	}
	if devs[1].Hostname != "tv" || devs[1].IP != "192.168.1.50" || devs[1].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("unexpected second: %+v", devs[1])
	}
}

func TestExpiredLeaseSkipped(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	now := time.Now()
	writeFile(t, leases, "1 aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n")
	r := &Reader{LeaseFile: leases, Now: func() time.Time { return now }}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 0 {
		t.Fatalf("expired lease must be skipped: %+v", devs)
	}
}

func TestMissingLeaseFileOK(t *testing.T) {
	r := &Reader{LeaseFile: filepath.Join(t.TempDir(), "nope")}
	devs, err := r.Devices()
	if err != nil {
		t.Fatalf("missing lease file must not error: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("want no devices, got %+v", devs)
	}
}

func TestUbusFallbackMerges(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	writeFile(t, leases, fmt.Sprintf("%d aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n", time.Now().Add(24*time.Hour).Unix()))
	// odhcpd-style response: bare-hex MAC, hostname, address, static flag.
	ubusJSON := `{"device":{"br-lan":{"leases":[
		{"mac":"aabbccddeeff","hostname":"tv","address":"192.168.1.50","flags":["bound","static"]},
		{"mac":"001122334455","hostname":"phone","address":"192.168.1.99","flags":[]},
		{"mac":"zz","hostname":"bad","address":"192.168.1.98","flags":[]}
	]}}}`
	r := &Reader{
		LeaseFile: leases,
		UbusCmd:   func() ([]byte, error) { return []byte(ubusJSON), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("tv (from file) + phone (from ubus) expected, got %d: %+v", len(devs), devs)
	}
	byHost := map[string]model.LANDevice{}
	for _, d := range devs {
		byHost[d.Hostname] = d
	}
	if byHost["tv"].IP != "192.168.1.50" {
		t.Fatalf("lease file must win for tv: %+v", byHost["tv"])
	}
	if byHost["phone"].MAC != "00:11:22:33:44:55" || !byHost["tv"].Static {
		t.Fatalf("ubus normalization/static flag wrong: %+v", byHost)
	}
}

func TestUbusErrorIgnored(t *testing.T) {
	r := &Reader{
		LeaseFile: filepath.Join(t.TempDir(), "nope"),
		UbusCmd:   func() ([]byte, error) { return nil, errors.New("ubus down") },
	}
	if _, err := r.Devices(); err != nil {
		t.Fatalf("ubus failure must be non-fatal: %v", err)
	}
}

func TestSetStaticDevStub(t *testing.T) {
	err := SetStatic(model.LANDevice{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: "tv"}, true)
	if !errors.Is(err, ErrDevStub) {
		t.Fatalf("dev mode must return ErrDevStub, got %v", err)
	}
	if err == nil || err.Error() == "" {
		t.Fatal("stub error must document the UCI commands")
	}
}

func TestSetStaticValidation(t *testing.T) {
	if err := SetStatic(model.LANDevice{MAC: "nope", IP: "192.168.1.5"}, false); err == nil {
		t.Fatal("bad MAC must fail")
	}
	if err := SetStatic(model.LANDevice{MAC: "aa:bb:cc:dd:ee:ff", IP: "nope"}, false); err == nil {
		t.Fatal("bad IP must fail")
	}
}

func TestNormMAC(t *testing.T) {
	cases := map[string]string{
		"aabbccddeeff":      "aa:bb:cc:dd:ee:ff",
		"AA:BB:CC:DD:EE:FF": "AA:BB:CC:DD:EE:FF",
		"":                  "",
		"xyz":               "",
		"aabbccddeef":       "", // wrong length
	}
	for in, want := range cases {
		if got := normMAC(in); got != want {
			t.Errorf("normMAC(%q) = %q, want %q", in, got, want)
		}
	}
}
