package nikki

import (
	"os"
	"path/filepath"
	"testing"
)

// Fake URLs only (secret rule: never real subscription URLs in tests).
const (
	fakeURL1 = "https://example.invalid/sub/one"
	fakeURL2 = "https://example.invalid/sub/two"
)

const runConfigFixture = `mixed-port: 7890
external-controller: 127.0.0.1:9090
proxy-providers:
  provider-a:
    type: http
    url: ` + fakeURL1 + `
    interval: 3600
    path: ./providers/a.yaml
  provider-b:
    type: file
    path: ./providers/b.yaml
proxies: []
`

const profileFixture = `proxy-providers:
  provider-c:
    type: http
    url: ` + fakeURL2 + `
  provider-d:
    type: http
    url: ` + fakeURL1 + `
`

const noProvidersFixture = `mixed-port: 7890
proxies:
  - name: x
    type: ss
`

func writeFixtures(t *testing.T) (dir string, paths []string) {
	t.Helper()
	dir = t.TempDir()
	run := filepath.Join(dir, "run-config.yaml")
	prof := filepath.Join(dir, "profile.yaml")
	if err := os.WriteFile(run, []byte(runConfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prof, []byte(profileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, []string{run, prof}
}

func TestDiscoverSubscriptionURLs(t *testing.T) {
	_, paths := writeFixtures(t)
	urls, skipped := DiscoverSubscriptionURLs(paths)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	// Unique + sorted: provider-d repeats provider-a's URL.
	if len(urls) != 2 {
		t.Fatalf("urls = %v, want 2 entries", urls)
	}
	if urls[0] != fakeURL1 || urls[1] != fakeURL2 {
		t.Fatalf("urls = %v, want sorted unique set", urls)
	}
}

func TestDiscoverSkipsMissingFiles(t *testing.T) {
	urls, skipped := DiscoverSubscriptionURLs([]string{"/nonexistent/nikki/config.yaml"})
	if len(urls) != 0 || skipped != 0 {
		t.Fatalf("missing file must be skipped silently: urls=%v skipped=%d", urls, skipped)
	}
}

func TestDiscoverNoProviders(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "no-providers.yaml")
	if err := os.WriteFile(p, []byte(noProvidersFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	urls, skipped := DiscoverSubscriptionURLs([]string{p})
	if len(urls) != 0 || skipped != 0 {
		t.Fatalf("no proxy-providers: urls=%v skipped=%d", urls, skipped)
	}
}

func TestDiscoverSkipsBrokenYAML(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("proxy-providers: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, []byte(profileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	urls, skipped := DiscoverSubscriptionURLs([]string{broken, good})
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (broken file)", skipped)
	}
	if len(urls) != 2 {
		t.Fatalf("urls = %v, want 2 from the good file", urls)
	}
}

func TestDefaultPathList(t *testing.T) {
	// On a dev host /etc/nikki does not exist: just the run config.
	paths := DefaultPathList()
	if len(paths) < 1 || paths[0] != RunConfigPath {
		t.Fatalf("DefaultPathList must start with %s, got %v", RunConfigPath, paths)
	}
	for _, p := range paths {
		if filepath.Dir(p) != "/etc/nikki/run" && filepath.Dir(p) != "/etc/nikki/profiles" {
			t.Fatalf("unexpected path outside /etc/nikki: %s", p)
		}
	}
}
