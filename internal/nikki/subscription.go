package nikki

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Subscription discovery (A3): read-only scan of the nikki
// mihomo config files for proxy-providers[].url values. WellBoard
// NEVER writes to /etc/nikki (invariant, see external rules);
// this file only reads.
//
// The owner's subscription URL is a SECRET: discovered values must not
// be printed to logs or tests. Handlers log counts and ids only.

// DefaultPaths lists the files scanned by DiscoverSubscriptionURLs
// when no explicit paths are given: the runtime config (RunConfigPath,
// shared with the external-rules reader in external.go) plus every
// profile YAML. The /etc/nikki/profiles/*.yaml glob is expanded by the
// caller (DefaultPathList returns it already expanded).
const (
	// ProfilesGlob is the profile-file pattern (read-only).
	ProfilesGlob = "/etc/nikki/profiles/*.yaml"
)

// DefaultPathList expands DefaultPaths into a concrete file list
// (missing files are skipped silently by the discovery itself).
func DefaultPathList() []string {
	paths := []string{RunConfigPath}
	matches, err := filepath.Glob(ProfilesGlob)
	if err == nil {
		paths = append(paths, matches...)
	}
	return paths
}

// DiscoverSubscriptionURLs parses each YAML file (missing files are
// skipped silently) and collects the unique, sorted proxy-providers
// url values that start with http:// or https://. Parse errors are
// skipped with a per-file count returned in the second value (the
// router may hold partially-broken profiles; a strict error would
// hide working subscriptions).
func DiscoverSubscriptionURLs(paths []string) (urls []string, skipped int) {
	seen := map[string]bool{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue // missing/unreadable file: nothing to import
		}
		found, err := providerURLs(data)
		if err != nil {
			skipped++
			continue
		}
		for _, u := range found {
			if !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
	}
	sort.Strings(urls)
	return urls, skipped
}

// providerURLs extracts http(s) urls from the top-level
// proxy-providers mapping of a mihomo config.
func providerURLs(data []byte) ([]string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	pp, ok := doc["proxy-providers"]
	if !ok {
		return nil, nil
	}
	providers, ok := pp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("proxy-providers is not a mapping")
	}
	var out []string
	for _, v := range providers {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		u, _ := entry["url"].(string)
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			out = append(out, u)
		}
	}
	return out, nil
}
