package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/wellboard/wellboard/internal/model"
)

// MergeResult describes what one merge did — surfaced for logs and tests.
type MergeResult struct {
	Added   int
	Updated int
	Stale   int
	Deleted int
}

// MatchKey is the 5.7.5 identity of a server within its source:
// (name, server, port). source_id is implicit — merge runs per source.
type MatchKey struct {
	Name   string
	Server string
	Port   string
}

// Key extracts the match key from a proxy dictionary. Port is normalized
// across int/string/float64 encodings (JSON/YAML decode differences).
func Key(p map[string]any) MatchKey {
	return MatchKey{
		Name:   asString(p["name"]),
		Server: asString(p["server"]),
		Port:   asString(p["port"]),
	}
}

// ServerID derives a stable server ID (FR-3.4) from the source and the
// match key: srv_<first 8 hex of sha256(sourceID|name|server|port)>. The
// same subscription maps to the same IDs across restarts and reinstalls.
func ServerID(sourceID string, key MatchKey) string {
	sum := sha256.Sum256([]byte(sourceID + "|" + key.Name + "|" + key.Server + "|" + key.Port))
	return "srv_" + hex.EncodeToString(sum[:])[:8]
}

// Merge folds the fetched proxies of one source into the state server
// pool (5.7.5). Servers are matched by (name, server, port) within the
// source:
//
//   - present servers are updated in place — ID, DelayMS and Stale flag
//     reset (FR-3.4 stable IDs; FR-4.8 routes keep working);
//   - previously-known servers absent now become Stale (consecutive miss
//     counter +1) and are deleted after StaleAfterUpdates misses; routes
//     on deleted IDs then surface "target lost" via the generator;
//   - new servers get stable derived IDs;
//   - servers of OTHER sources are untouched.
//
// Miss counting lives in model.Server.StaleMisses (schema v2); the
// migration in the store seeds it from the old raw-map encoding.
func Merge(st *model.State, sourceID string, proxies []map[string]any) MergeResult {
	res := MergeResult{}

	// Index current servers of this source by match key.
	current := map[MatchKey]int{}
	for i, s := range st.Servers {
		if s.SourceID != sourceID {
			continue
		}
		current[MatchKey{Name: s.Name, Server: rawStr(s.Raw, "server"), Port: rawStr(s.Raw, "port")}] = i
	}

	seen := map[MatchKey]bool{}
	for _, p := range proxies {
		key := Key(p)
		if key.Name == "" || key.Server == "" {
			continue // unusable entry
		}
		if seen[key] {
			continue // duplicate entry in one answer
		}
		seen[key] = true

		raw := cloneRaw(p)
		typ := asString(p["type"])

		if idx, ok := current[key]; ok {
			srv := &st.Servers[idx]
			srv.Raw = raw
			srv.Type = typ
			srv.Name = key.Name
			srv.Stale = false
			srv.StaleMisses = 0
			res.Updated++
			continue
		}
		st.Servers = append(st.Servers, model.Server{
			ID:          ServerID(sourceID, key),
			SourceID:    sourceID,
			Name:        key.Name,
			Type:        typ,
			Raw:         raw,
			StaleMisses: 0,
		})
		res.Added++
	}

	// Rebuild: vanished servers go stale (miss counter), deleted at the
	// window; everything else (incl. other sources) passes through.
	next := make([]model.Server, 0, len(st.Servers))
	for i := range st.Servers {
		s := st.Servers[i]
		if s.SourceID != sourceID {
			next = append(next, s)
			continue
		}
		key := MatchKey{Name: s.Name, Server: rawStr(s.Raw, "server"), Port: rawStr(s.Raw, "port")}
		if seen[key] {
			next = append(next, s)
			continue
		}
		miss := s.StaleMisses + 1
		if miss >= StaleAfterUpdates {
			res.Deleted++
			continue
		}
		s.Stale = true
		s.StaleMisses = miss
		next = append(next, s)
		res.Stale++
	}
	st.Servers = next
	return res
}

// StaleServerIDs returns the IDs of stale servers of the given source
// (routes pointing at them are "target lost", FR-4.8).
func StaleServerIDs(st *model.State, sourceID string) []string {
	var out []string
	for _, s := range st.Servers {
		if s.SourceID == sourceID && s.Stale {
			out = append(out, s.ID)
		}
	}
	sort.Strings(out)
	return out
}

// cloneRaw copies the proxy map so later state mutations cannot alias the
// parsed document.
func cloneRaw(p map[string]any) map[string]any {
	raw := make(map[string]any, len(p))
	for k, v := range p {
		raw[k] = v
	}
	return raw
}

// rawStr reads a normalized string field from the stored raw proxy map.
func rawStr(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	return asString(raw[key])
}

// asString renders a JSON/YAML-decoded value as a stable string (ports
// arrive as int, string or float64 depending on the decoding path).
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return fmt.Sprintf("%v", t)
	case uint16:
		return strconv.Itoa(int(t))
	case int32:
		return strconv.Itoa(int(t))
	default:
		return fmt.Sprintf("%v", t)
	}
}
