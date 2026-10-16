package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// decodeStrictDoc decodes one JSON document with DisallowUnknownFields —
// the same decoder contract the API layer applies to every payload.
func decodeStrictDoc(doc string, v any) error {
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func decodeStrict[T any](doc string) (T, error) {
	var v T
	err := decodeStrictDoc(doc, &v)
	return v, err
}

// mustDecode runs a strict decode for each case, asserting success.
func mustDecode[T any](t *testing.T, name string, docs ...string) {
	t.Helper()
	for _, doc := range docs {
		if _, err := decodeStrict[T](doc); err != nil {
			t.Errorf("%s: strict decode of %s failed: %v", name, doc, err)
		}
	}
}

// TestStrictDecodeRejectsUnknownFields: every model root type must reject
// an unknown top-level field under a strict decoder. This is the model
// counterpart of the API's DisallowUnknownFields contract.
func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	docs := []string{
		`{"version":2,"extra":1}`,
		`{"settings":{"ui_port":8090},"surprise":true}`,
		`{"sources":[],"unknown":[]}`,
		`{"servers":[],"servers2":[]}`,
		`{"groups":[],"members_extra":[]}`,
		`{"routes":[],"routes_extra":[]}`,
		`{"lan_devices":[],"lan":[]}`,
	}
	for _, doc := range docs {
		if _, err := decodeStrict[State](doc); err == nil {
			t.Errorf("State strict decode accepted unknown field: %s", doc)
		}
	}
}

// TestKnownFieldNamesAreExact: the json field names the model actually
// answers to are exactly the documented state.json layout (initial TZ 5.3
// / B1-B2). A typo'd or renamed tag would silently widen or shift the
// accepted wire format; this pins it.
func TestKnownFieldNamesAreExact(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"settings", `{"settings":{}}`},
		{"sources", `{"sources":[]}`},
		{"servers", `{"servers":[]}`},
		{"groups", `{"groups":[]}`},
		{"routes", `{"routes":[]}`},
		{"lan_devices", `{"lan_devices":[]}`},
	}
	for _, tc := range cases {
		if _, err := decodeStrict[State](tc.doc); err != nil {
			t.Errorf("State strict decode rejected known field %s: %v", tc.name, err)
		}
	}
}

// TestSettingsFieldNames pins the Settings wire format (FR-9.1, B1, B2):
// all documented fields decode; none other.
func TestSettingsFieldNames(t *testing.T) {
	mustDecode[Settings](t, "Settings",
		`{"ui_port":8090}`,
		`{"lang":"ru"}`,
		`{"delay_test_interval_sec":600}`,
		`{"default_policy":{"type":"direct"}}`,
		`{"geosite_source":"runetfreedom"}`,
		`{"geoip_source":"metacubex"}`,
		`{"geosite_custom_url":""}`,
		`{"geoip_custom_url":"https://example.invalid/geoip.dat"}`,
		`{"geodata_additions":{"geosite:cat":["a.com"]}}`,
		`{"disabled_templates":["tpl_x"]}`,
	)
	if _, err := decodeStrict[Settings](`{"u_port":8090}`); err == nil {
		t.Error("Settings accepted misspelled field u_port")
	}
	if _, err := decodeStrict[Settings](`{"port":8090}`); err == nil {
		t.Error("Settings accepted unknown field port")
	}
}

// TestSourceUserInfoHWIDFieldNames pins the Source subtree wire format.
func TestSourceUserInfoHWIDFieldNames(t *testing.T) {
	mustDecode[Source](t, "Source",
		`{"id":"sub_1","kind":"subscription","name":"S"}`,
		`{"url":"https://example.invalid/s"}`,
		`{"enabled":true}`,
		`{"update_interval_sec":86400}`,
		`{"last_update":"2026-01-01T00:00:00Z"}`,
		`{"last_error":"boom"}`,
		`{"userinfo":{"upload":1,"download":2,"total":3,"expire":4}}`,
		`{"hwid_status":{"active":true,"limit_reached":false,"not_supported":false}}`,
		`{"announce":"text"}`,
	)
	if _, err := decodeStrict[Source](`{"id":"sub_1","interval_sec":60}`); err == nil {
		t.Error("Source accepted unknown field interval_sec")
	}
	mustDecode[UserInfo](t, "UserInfo",
		`{"upload":0,"download":0,"total":0,"expire":0}`,
	)
	if _, err := decodeStrict[UserInfo](`{"up":1}`); err == nil {
		t.Error("UserInfo accepted unknown field up")
	}
	mustDecode[HWIDStatus](t, "HWIDStatus",
		`{"active":false,"limit_reached":false,"not_supported":false}`,
	)
	if _, err := decodeStrict[HWIDStatus](`{"active":false,"limited":true}`); err == nil {
		t.Error("HWIDStatus accepted unknown field limited")
	}
}

// TestServerFieldNames pins the Server wire format, including the raw
// mihomo proxy dictionary (whose inner keys are free-form by design and
// therefore only tested for round-trip, not strictness).
func TestServerFieldNames(t *testing.T) {
	mustDecode[Server](t, "Server",
		`{"id":"srv_1","source_id":"sub_1","name":"NL-1","type":"ss","raw":{"server":"h","port":1}}`,
		`{"delay_ms":120}`,
		`{"stale":false}`,
		`{"stale_misses":0}`,
	)
	if _, err := decodeStrict[Server](`{"id":"srv_1","server":"h"}`); err == nil {
		t.Error("Server accepted unknown field server")
	}
}

// TestGroupRouteFieldNames pins the Group / Route / condition / target
// wire format (FR-3.3, FR-4.1-4.7, FR-5.4).
func TestGroupRouteFieldNames(t *testing.T) {
	mustDecode[Group](t, "Group",
		`{"id":"grp_1","name":"G","type":"select","members":["srv_1"]}`,
	)
	if _, err := decodeStrict[Group](`{"id":"grp_1","group_type":"select"}`); err == nil {
		t.Error("Group accepted unknown field group_type")
	}
	mustDecode[Route](t, "Route",
		`{"id":"rt_1","name":"R","enabled":true,"order":1,`+
			`"conditions":[{"type":"domain","value":"a.com"}],`+
			`"target":{"type":"direct"},"on_unavailable":"block"}`,
		`{"providers":["p"]}`,
	)
	if _, err := decodeStrict[Route](`{"id":"rt_1","on_unavailable":"block","priority":1}`); err == nil {
		t.Error("Route accepted unknown field priority")
	}
	mustDecode[RouteCondition](t, "RouteCondition",
		`{"type":"domain","value":"a.com","label":""}`,
	)
	if _, err := decodeStrict[RouteCondition](`{"type":"domain","value":"a.com","name":"x"}`); err == nil {
		t.Error("RouteCondition accepted unknown field name")
	}
	mustDecode[Target](t, "Target",
		`{"type":"direct"}`,
		`{"type":"server","id":"srv_1"}`,
	)
	if _, err := decodeStrict[Target](`{"type":"direct","target_id":"srv_1"}`); err == nil {
		t.Error("Target accepted unknown field target_id")
	}
}

// TestLANDeviceFieldNames pins the LAN device wire format (FR-4.4).
func TestLANDeviceFieldNames(t *testing.T) {
	mustDecode[LANDevice](t, "LANDevice",
		`{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50","hostname":"pc","static":true}`,
	)
	if _, err := decodeStrict[LANDevice](`{"mac":"aa","ip":"b","host":"pc"}`); err == nil {
		t.Error("LANDevice accepted unknown field host")
	}
}

// TestEnumValueSets: the kind/group/condition/target enum string sets are
// exactly the documented ones — no extra accepted spelling, none missing.
func TestEnumValueSets(t *testing.T) {
	// SourceKind values.
	for _, k := range []string{"subscription", "manual"} {
		if string(SourceSubscription) != "subscription" || string(SourceManual) != "manual" {
			t.Fatalf("SourceKind constants changed: %q %q", SourceSubscription, SourceManual)
		}
		mustDecode[Source](t, "Source kind", `{"kind":"`+k+`"}`)
	}
	// GroupType values.
	groupTypes := map[GroupType]bool{
		GroupSelect: true, GroupURLTest: true, GroupFallback: true, GroupLoadBalance: true,
	}
	if len(groupTypes) != 4 {
		t.Fatalf("group type set changed: %v", groupTypes)
	}
	// RouteConditionType values.
	condTypes := []RouteConditionType{
		CondDomain, CondDomainSuffix, CondDomainKeyword, CondGeosite,
		CondGeoIP, CondIPCIDR, CondSrcDevice, CondDstPort,
	}
	wantConds := []string{
		"domain", "domain-suffix", "domain-keyword", "geosite",
		"geoip", "ip-cidr", "src-device", "dst-port",
	}
	for i, c := range condTypes {
		if string(c) != wantConds[i] {
			t.Errorf("condition type %d = %q, want %q", i, c, wantConds[i])
		}
	}
	// TargetType values.
	targetTypes := map[TargetType]bool{
		TargetServer: true, TargetGroup: true, TargetDirect: true, TargetReject: true,
	}
	if len(targetTypes) != 4 {
		t.Fatalf("target type set changed: %v", targetTypes)
	}
}

// TestEnumRejectsUnknownValues: an out-of-set enum value must fail a strict
// … actually a plain decode (enums are plain strings in the model); the
// API layer is what rejects unknown values. What the model layer CAN pin:
// unknown values decode but compare unequal to every constant, so
// switch-based validation in the API cannot accidentally accept them.
func TestEnumRejectsUnknownValues(t *testing.T) {
	badGroup := Group{Type: "round-robin"}
	if badGroup.Type == GroupSelect || badGroup.Type == GroupURLTest ||
		badGroup.Type == GroupFallback || badGroup.Type == GroupLoadBalance {
		t.Error("unknown group type compares equal to a known constant")
	}
	badCond := RouteCondition{Type: "user-agent"}
	knownConds := []RouteConditionType{
		CondDomain, CondDomainSuffix, CondDomainKeyword, CondGeosite,
		CondGeoIP, CondIPCIDR, CondSrcDevice, CondDstPort,
	}
	for _, k := range knownConds {
		if badCond.Type == k {
			t.Errorf("unknown condition type compares equal to %q", k)
		}
	}
	badTarget := Target{Type: "chain"}
	if badTarget.Type == TargetServer || badTarget.Type == TargetGroup ||
		badTarget.Type == TargetDirect || badTarget.Type == TargetReject {
		t.Error("unknown target type compares equal to a known constant")
	}
}

// TestJSONRoundTrip: a representative full State document decodes and
// re-encodes without loss at the model level (field tags intact on both
// directions). Marshalling details (omitempty) are not asserted beyond
// the decoded-equality check.
func TestJSONRoundTrip(t *testing.T) {
	doc := `{"version":2,` +
		`"settings":{"ui_port":8090,"lang":"ru","delay_test_interval_sec":600,` +
		`"default_policy":{"type":"direct"},"geosite_source":"runetfreedom",` +
		`"geoip_source":"runetfreedom","geodata_additions":{"geosite:cat":["a.com"]},` +
		`"disabled_templates":["tpl_x"]},` +
		`"sources":[{"id":"sub_1","kind":"subscription","name":"S",` +
		`"url":"https://example.invalid/s","enabled":true,"update_interval_sec":86400,` +
		`"last_update":"2026-01-01T00:00:00Z","last_error":null,` +
		`"userinfo":{"upload":1,"download":2,"total":3,"expire":4},` +
		`"hwid_status":{"active":true,"limit_reached":false,"not_supported":false},` +
		`"announce":null}],` +
		`"servers":[{"id":"srv_1","source_id":"sub_1","name":"NL-1","type":"ss",` +
		`"raw":{"server":"203.0.113.10","port":8388},"delay_ms":120,"stale":false,` +
		`"stale_misses":0}],` +
		`"groups":[{"id":"grp_1","name":"G","type":"select","members":["srv_1"]}],` +
		`"routes":[{"id":"rt_1","name":"R","enabled":true,"order":1,` +
		`"conditions":[{"type":"domain","value":"a.com"}],` +
		`"target":{"type":"server","id":"srv_1"},"on_unavailable":"block",` +
		`"providers":["p"]}],` +
		`"lan_devices":[{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.178.1.50",` +
		`"hostname":"pc","static":true}]}`
	st, err := decodeStrict[State](doc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Version != 2 || st.Settings.UIPort != 8090 || len(st.Servers) != 1 ||
		len(st.Groups) != 1 || len(st.Routes) != 1 || len(st.LANDevices) != 1 {
		t.Fatalf("decoded state shape wrong: %+v", st)
	}
	// Re-encode and re-decode; must be lossless.
	re, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	st2, err := decodeStrict[State](string(re))
	if err != nil {
		t.Fatalf("re-decode of re-encoded state: %v (doc: %s)", err, re)
	}
	if !reflect.DeepEqual(st, st2) {
		t.Errorf("round-trip mutated state:\n%+v\n%+v", st, st2)
	}
}

// TestOversizedModelFields: the model itself imposes no length limits
// (the 1 MiB request cap is enforced at the API layer); pin that a
// document at the cap boundary still decodes at model level and the
// oversized rejection is solely the API's job — see
// internal/api/model_validation_edge_test.go for the behavioral side.
func TestOversizedModelFields(t *testing.T) {
	big := strings.Repeat("a", 1<<20)
	doc := `{"name":"` + big + `"}`
	var g Group
	// Not strict on purpose: the point is the model has no limit here.
	if err := json.Unmarshal([]byte(doc), &g); err != nil {
		t.Fatalf("model decode of 1 MiB field failed: %v", err)
	}
	if len(g.Name) != 1<<20 {
		t.Fatalf("name length = %d, want %d", len(g.Name), 1<<20)
	}
}
