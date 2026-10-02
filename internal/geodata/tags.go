package geodata

import (
	"os"
	"sort"
	"strings"
)

// defaultTags is the curated fallback category list served by
// KnownGeositeTags when no local .dat file is found (B3):
// ~30 popular v2fly/geosite categories, lowercase like the UI uses
// them (the .dat tags are uppercase; the endpoint lowercases for the
// datalist, mihomo matches case-insensitively).
var defaultTags = []string{
	"ru", "category-ru-gov", "youtube", "google", "netflix", "discord",
	"telegram", "twitter", "facebook", "instagram", "tiktok", "openai",
	"anthropic", "github", "gitlab", "steam", "epicgames", "reddit",
	"wikipedia", "cloudflare", "speedtest", "paypal", "visa",
	"mastercard", "category-ads-all", "twitch", "spotify", "disney",
	"hbo", "category-gov-ru", "vk", "yandex", "mailru", "rutracker",
}

// KnownGeositeTags returns the geosite category tags to offer in the
// template editor datalist: the tags parsed from the first readable
// .dat among datPaths (typically the nikki run dir geosite.dat or a
// cached copy), or the curated static list when none is found. Tags
// are returned lowercase, deduplicated, sorted.
func KnownGeositeTags(datPaths []string) []string {
	for _, p := range datPaths {
		data, err := os.ReadFile(p) // #nosec G304 -- datPaths are nikki run-dir paths collected by the adapter, not user-supplied
		if err != nil {
			continue
		}
		tags, ok := ParseTags(data)
		if !ok || len(tags) == 0 {
			continue
		}
		return lowerSorted(tags)
	}
	out := make([]string, len(defaultTags))
	copy(out, defaultTags)
	sort.Strings(out)
	return out
}

// lowerSorted converts the tag set to a sorted lowercase slice.
func lowerSorted(tags map[string]bool) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for t := range tags {
		low := strings.ToLower(t)
		if !seen[low] {
			seen[low] = true
			out = append(out, low)
		}
	}
	sort.Strings(out)
	return out
}
