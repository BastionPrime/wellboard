// Package model defines the WellBoard state data model (initial TZ 5.3).
//
// Everything inside the application is referenced by stable ID
// (srv_xxx / grp_xxx / rt_x / sub_xxx); display names may change without
// breaking route references (FR-3.4). The state.json layout mirrors the
// example in the initial TZ, section 5.3.
package model

// State is the root of state.json. Version is the schema version; the store
// migrates older files forward (initial TZ 7, Phase 1).
type State struct {
	Version    int         `json:"version"`
	Settings   Settings    `json:"settings"`
	Sources    []Source    `json:"sources"`
	Servers    []Server    `json:"servers"`
	Groups     []Group     `json:"groups"`
	Routes     []Route     `json:"routes"`
	LANDevices []LANDevice `json:"lan_devices"`
}

// Settings holds the global user preferences (initial TZ 5.3, FR-9.1).
type Settings struct {
	// UIPort is the WellBoard web UI port (decision Q6, default 8090).
	UIPort int `json:"ui_port"`
	// Lang is the UI language: "ru" (default) or "en" (decision Q11).
	Lang string `json:"lang"`
	// Geodata is the geodata source: "runetfreedom" (default) or "metacubex"
	// (decision Q7, FR-5.4).
	Geodata string `json:"geodata"`
	// DefaultPolicy is the MATCH policy for traffic matched by no rule
	// (FR-4.6, decision Q3: user-configurable, default DIRECT).
	DefaultPolicy Target `json:"default_policy"`
	// DelayTestIntervalSec is how often proxy groups re-test latency
	// (FR-9.1).
	DelayTestIntervalSec int `json:"delay_test_interval_sec"`
}

// SourceKind distinguishes subscriptions from the manual entry source.
type SourceKind string

const (
	// SourceSubscription is a remote subscription (FR-1.1).
	SourceSubscription SourceKind = "subscription"
	// SourceManual is the built-in bucket for hand-added servers (FR-1.3).
	SourceManual SourceKind = "manual"
)

// Source is one server source: a subscription or the manual bucket.
type Source struct {
	ID                string      `json:"id"`
	Kind              string      `json:"kind"`
	Name              string      `json:"name"`
	URL               string      `json:"url,omitempty"`
	Enabled           bool        `json:"enabled,omitempty"`
	UpdateIntervalSec int         `json:"update_interval_sec,omitempty"`
	LastUpdate        string      `json:"last_update,omitempty"`
	LastError         *string     `json:"last_error,omitempty"`
	UserInfo          *UserInfo   `json:"userinfo,omitempty"`
	HWIDStatus        *HWIDStatus `json:"hwid_status,omitempty"`
	Announce          *string     `json:"announce,omitempty"`
}

// UserInfo carries the subscription traffic quota (FR-7.1).
type UserInfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	// Expire is a unix timestamp; 0 = unknown.
	Expire int64 `json:"expire"`
}

// HWIDStatus is the device-limit state of a subscription (FR-2.4).
type HWIDStatus struct {
	Active       bool `json:"active"`
	LimitReached bool `json:"limit_reached"`
	NotSupported bool `json:"not_supported"`
}

// Server is one proxy in the unified pool (FR-3.1). Raw holds the mihomo
// proxy dictionary verbatim — WellBoard does not interpret proxy fields.
type Server struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	// Raw is the mihomo proxy map as received from the subscription /
	// converter (initial TZ 5.3: "proxy-словарь mihomo как есть").
	Raw map[string]any `json:"raw"`
	// DelayMS is the last measured latency (FR-3.1); 0 = never tested.
	DelayMS int `json:"delay_ms,omitempty"`
	// Stale marks a server that vanished from its subscription (FR-4.8):
	// it is excluded from generation; routes on it report "target lost".
	Stale bool `json:"stale,omitempty"`
	// StaleMisses counts consecutive updates the server was absent
	// (initial TZ 5.7.5: delete after 3). Reset when it reappears.
	// Schema v2 (Phase 2); migrated from the Phase 1 raw-map encoding.
	StaleMisses int `json:"stale_misses,omitempty"`
}

// GroupType is the mihomo proxy-group strategy (FR-3.3).
type GroupType string

const (
	GroupSelect      GroupType = "select"
	GroupURLTest     GroupType = "url-test"
	GroupFallback    GroupType = "fallback"
	GroupLoadBalance GroupType = "load-balance"
)

// Group is a user-defined proxy group (FR-3.3). Members reference server or
// group IDs; the generator resolves them to mihomo proxy names.
type Group struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Type GroupType `json:"type"`
	// Members are server/group IDs in priority order (fallback order).
	Members []string `json:"members"`
}

// Route is one routing rule set (FR-4.1): ordered conditions + target +
// availability policy.
type Route struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Order defines the rules position among other routes (FR-4.5); the
	// generator emits rules in ascending order, ties broken by ID for
	// determinism.
	Order int `json:"order"`
	// Conditions is the AND set of match conditions (FR-4.2).
	Conditions []RouteCondition `json:"conditions"`
	// Target is the route destination (FR-4.3).
	Target Target `json:"target"`
	// OnUnavailable is the fail behavior when the target is down (FR-4.7,
	// decision Q4): "block" (default) or "direct".
	OnUnavailable string `json:"on_unavailable"`
	// Providers names local fallback rule-provider lists (FR-5.4) shipped
	// in templates/providers/<name>.yaml. Filled when the route was
	// created from a template; the generator emits a RULE-SET line per
	// provider in addition to the condition lines. The route stays an
	// ordinary editable route (FR-5.2).
	Providers []string `json:"providers,omitempty"`
}

// RouteConditionType enumerates the supported condition kinds (FR-4.2).
type RouteConditionType string

const (
	CondDomain        RouteConditionType = "domain"
	CondDomainSuffix  RouteConditionType = "domain-suffix"
	CondDomainKeyword RouteConditionType = "domain-keyword"
	CondGeosite       RouteConditionType = "geosite"
	CondGeoIP         RouteConditionType = "geoip"
	CondIPCIDR        RouteConditionType = "ip-cidr"
	CondSrcDevice     RouteConditionType = "src-device"
	CondDstPort       RouteConditionType = "dst-port"
)

// RouteCondition is a single match condition. For src-device the value is
// the LAN device IP in CIDR form (e.g. "192.168.1.50/32") plus a Label.
type RouteCondition struct {
	Type  RouteConditionType `json:"type"`
	Value string             `json:"value"`
	// Label is the human-readable device name for src-device conditions.
	Label string `json:"label,omitempty"`
}

// TargetType is what a route or the default policy points at (FR-4.3).
type TargetType string

const (
	// TargetServer points at a specific proxy by ID.
	TargetServer TargetType = "server"
	// TargetGroup points at a user group by ID.
	TargetGroup TargetType = "group"
	// TargetDirect is the DIRECT pseudo-proxy.
	TargetDirect TargetType = "direct"
	// TargetReject is the REJECT pseudo-proxy.
	TargetReject TargetType = "reject"
)

// Target resolves to a proxy, group, DIRECT or REJECT.
type Target struct {
	Type TargetType `json:"type"`
	// ID references a server or group when Type is server|group.
	ID string `json:"id,omitempty"`
}

// LANDevice is one device from the DHCP leases (FR-4.4).
type LANDevice struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	// Static is true when the lease is pinned in UCI dhcp (FR-4.4).
	Static bool `json:"static"`
}
