// Package api contains settings handlers.
package api

import (
	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/geodata"
	"github.com/wellboard/wellboard/internal/model"
	"net/http"
	"strconv"
	"strings"
)

type settingsPatch struct {
	UIPort               *int                 `json:"ui_port"`
	Lang                 *string              `json:"lang"`
	GeositeSource        *string              `json:"geosite_source"`
	GeoipSource          *string              `json:"geoip_source"`
	GeositeCustomURL     *string              `json:"geosite_custom_url"`
	GeoipCustomURL       *string              `json:"geoip_custom_url"`
	GeodataAdditions     *map[string][]string `json:"geodata_additions"`
	DefaultPolicy        *model.Target        `json:"default_policy"`
	DelayTestIntervalSec *int                 `json:"delay_test_interval_sec"`
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	// A3: the Settings PAGE shows sources with a masked URL
	// (secret rule: the full URL is not printed on the settings
	// screen). The shape of the settings object itself is unchanged —
	// sources_summary is an additional field.
	summary := sourcesSummary(st.Sources)
	writeJSON(w, http.StatusOK, settingsOut{
		Settings:       st.Settings,
		SourcesSummary: summary,
	})
}

// sourceSummary is one masked sources-list row for the settings page.
type sourceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MaskedURL string `json:"masked_url"`
	Enabled   bool   `json:"enabled"`
}

// settingsOut wraps the settings object with the masked sources
// summary (extra field, existing keys unchanged).
type settingsOut struct {
	model.Settings
	SourcesSummary []sourceSummary `json:"sources_summary"`
}

// maskURL renders a display-safe URL: the first 10 characters plus
// the total length when long enough; "****" for short/empty values
// (a real http(s) URL is always ≥ 10 chars, so short means garbage).
func maskURL(u string) string {
	if len(u) > 14 {
		return u[:10] + "…(" + strconv.Itoa(len(u)) + ")"
	}
	return "****"
}

// sourcesSummary maps sources onto the masked rows used by every
// non-/sources API surface (the raw URL stays in the existing /sources
// route only, as it always has).
func sourcesSummary(sources []model.Source) []sourceSummary {
	out := make([]sourceSummary, 0, len(sources))
	for _, src := range sources {
		out = append(out, sourceSummary{
			ID: src.ID, Name: src.Name, Kind: src.Kind,
			MaskedURL: maskURL(src.URL), Enabled: src.Enabled,
		})
	}
	return out
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var in settingsPatch
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	set := &st.Settings
	if in.UIPort != nil {
		if *in.UIPort < 1 || *in.UIPort > 65535 {
			writeError(w, httpErr(http.StatusBadRequest, "ui_port must be 1-65535"))
			return
		}
		set.UIPort = *in.UIPort
	}
	if in.Lang != nil {
		switch *in.Lang {
		case "ru", "en":
			set.Lang = *in.Lang
		default:
			writeError(w, httpErr(http.StatusBadRequest, "lang must be \"ru\" or \"en\""))
			return
		}
	}
	if in.GeositeSource != nil {
		if !geodata.GeositeValid(*in.GeositeSource) {
			writeError(w, httpErr(http.StatusBadRequest, "geosite_source must be \"runetfreedom\", \"metacubex\" or \"custom\""))
			return
		}
		set.GeositeSource = *in.GeositeSource
	}
	if in.GeoipSource != nil {
		if !geodata.GeoipValid(*in.GeoipSource) {
			writeError(w, httpErr(http.StatusBadRequest, "geoip_source must be \"runetfreedom\", \"metacubex\" or \"custom\""))
			return
		}
		set.GeoipSource = *in.GeoipSource
	}
	if in.GeositeCustomURL != nil {
		if *in.GeositeCustomURL != "" && !strings.HasPrefix(*in.GeositeCustomURL, "http://") && !strings.HasPrefix(*in.GeositeCustomURL, "https://") {
			writeError(w, httpErr(http.StatusBadRequest, "geosite_custom_url must be http(s)"))
			return
		}
		set.GeositeCustomURL = *in.GeositeCustomURL
	}
	if in.GeoipCustomURL != nil {
		if *in.GeoipCustomURL != "" && !strings.HasPrefix(*in.GeoipCustomURL, "http://") && !strings.HasPrefix(*in.GeoipCustomURL, "https://") {
			writeError(w, httpErr(http.StatusBadRequest, "geoip_custom_url must be http(s)"))
			return
		}
		set.GeoipCustomURL = *in.GeoipCustomURL
	}
	// Full replace of the additions map (B2). Custom-source
	// URL presence is enforced at GENERATION time (the profile is
	// where a custom source without a URL becomes an error) — here we
	// validate what can be validated: source values and entry formats.
	if in.GeodataAdditions != nil {
		for key, entries := range *in.GeodataAdditions {
			kind, cat, ok := strings.Cut(key, ":")
			if !ok || cat == "" {
				writeError(w, httpErr(http.StatusBadRequest, "geodata_additions key %q must be \"geosite:<category>\" or \"geoip:<category>\"", key))
				return
			}
			if kind != "geosite" && kind != "geoip" {
				writeError(w, httpErr(http.StatusBadRequest, "geodata_additions key %q must start with \"geosite:\" or \"geoip:\"", key))
				return
			}
			for _, v := range entries {
				if err := validateGeodataAdditionEntry(kind, v); err != nil {
					writeError(w, httpErr(http.StatusBadRequest, "geodata_additions[%s]: %v", key, err))
					return
				}
			}
		}
		set.GeodataAdditions = *in.GeodataAdditions
	}
	if in.DefaultPolicy != nil {
		if err := validateTarget(st, *in.DefaultPolicy); err != nil {
			writeError(w, err)
			return
		}
		set.DefaultPolicy = *in.DefaultPolicy
	}
	if in.DelayTestIntervalSec != nil {
		if *in.DelayTestIntervalSec < 0 {
			writeError(w, httpErr(http.StatusBadRequest, "delay_test_interval_sec must be ≥ 0"))
			return
		}
		set.DelayTestIntervalSec = *in.DelayTestIntervalSec
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

// ----------------------------------------------------------------------------
// Profile preview (FR-4.9)
// ----------------------------------------------------------------------------

// handleProfilePreview renders the current state as the generated
// mihomo profile (read-only, for the UI debug view). Generator
// *Problems (lost targets, unknown providers) surface as 409 with the
// problem list.
func (s *Server) handleProfilePreview(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	out, err := generator.GenerateWithProviders(st, s.knownProviders())
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// knownProviders returns the set of provider names with a local payload
// file in the loaded template catalog (nil = check disabled, catalog
// unavailable in dev mode). Route provider references are validated
// against this set during generation (review follow-up: unknown
// provider must surface, not silently 200).
func (s *Server) knownProviders() map[string]bool {
	if s.Catalog == nil {
		return nil
	}
	m := make(map[string]bool, len(s.Catalog.Templates))
	for _, tpl := range s.Catalog.Templates {
		for _, p := range tpl.Providers {
			m[p] = true
		}
	}
	return m
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// errDevStubSentinel matches the lan dev-stub error without importing
// the lan package here (avoided a dependency cycle: lan imports nothing
// from api; matching by variable identity is enough — set by SetLAN).
var errDevStubSentinel error

// SetDevStubSentinel wires the lan.ErrDevStub identity into the server.
func SetDevStubSentinel(err error) { errDevStubSentinel = err }
