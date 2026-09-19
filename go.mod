module github.com/wellboard/wellboard

go 1.22

require gopkg.in/yaml.v3 v3.0.1

// Dependencies policy (docs/DECISIONS.md, decision D2; initial TZ 5.2):
// stdlib + gopkg.in/yaml.v3 only. The yaml dependency landed in Phase 1
// with the mihomo profile generator (initial TZ 7). The mihomo convert
// package is planned for Phase 2 and must be re-evaluated then.
