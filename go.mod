module github.com/wellboard/wellboard

go 1.22

// Dependencies policy (docs/DECISIONS.md, decision D2; initial TZ 5.2):
// stdlib only in Phase 0 — nothing imports YAML yet. gopkg.in/yaml.v3 is the
// only sanctioned external dependency and lands together with the mihomo
// profile generator in Phase 1 (an unused require would be dropped by
// `go mod tidy`, so it is deliberately absent here).
