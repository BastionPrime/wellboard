package api

import (
	"net/http"
)

// Register mounts all Phase 3 routes on mux (method-patterned; net/http
// answers other methods with 405).
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/state", s.handleState)
	mux.HandleFunc("GET /api/v1/sources", s.handleSourcesList)
	mux.HandleFunc("POST /api/v1/sources", s.handleSourcesCreate)
	mux.HandleFunc("POST /api/v1/sources/import-nikki", s.handleSourcesImportNikki)
	mux.HandleFunc("GET /api/v1/sources/{id}", s.handleSourceGet)
	mux.HandleFunc("PATCH /api/v1/sources/{id}", s.handleSourcePatch)
	mux.HandleFunc("DELETE /api/v1/sources/{id}", s.handleSourceDelete)
	mux.HandleFunc("GET /api/v1/servers", s.handleServersList)
	mux.HandleFunc("POST /api/v1/servers", s.handleServersCreate)
	mux.HandleFunc("GET /api/v1/servers/{id}", s.handleServerGet)
	mux.HandleFunc("PUT /api/v1/servers/{id}", s.handleServerUpdate)
	mux.HandleFunc("DELETE /api/v1/servers/{id}", s.handleServerDelete)
	mux.HandleFunc("GET /api/v1/groups", s.handleGroupsList)
	mux.HandleFunc("POST /api/v1/groups", s.handleGroupsCreate)
	mux.HandleFunc("GET /api/v1/groups/{id}", s.handleGroupGet)
	mux.HandleFunc("PATCH /api/v1/groups/{id}", s.handleGroupPatch)
	mux.HandleFunc("DELETE /api/v1/groups/{id}", s.handleGroupDelete)
	mux.HandleFunc("GET /api/v1/routes", s.handleRoutesList)
	mux.HandleFunc("POST /api/v1/routes", s.handleRoutesCreate)
	mux.HandleFunc("GET /api/v1/routes/{id}", s.handleRouteGet)
	mux.HandleFunc("PATCH /api/v1/routes/{id}", s.handleRoutePatch)
	mux.HandleFunc("DELETE /api/v1/routes/{id}", s.handleRouteDelete)
	// External nikki rules: read-only view of the active
	// mihomo config + explicit per-rule import into WellBoard state.
	mux.HandleFunc("GET /api/v1/external-rules", s.handleExternalRulesList)
	mux.HandleFunc("POST /api/v1/external-rules/import", s.handleExternalRuleImport)
	mux.HandleFunc("POST /api/v1/external-rules/import-all", s.handleExternalRulesImportAll)
	mux.HandleFunc("GET /api/v1/templates", s.handleTemplatesList)
	mux.HandleFunc("POST /api/v1/templates", s.handleTemplatesCreate)
	mux.HandleFunc("GET /api/v1/templates/{id}", s.handleTemplatesGet)
	mux.HandleFunc("PUT /api/v1/templates/{id}", s.handleTemplatesUpdate)
	mux.HandleFunc("DELETE /api/v1/templates/{id}", s.handleTemplatesDelete)
	mux.HandleFunc("POST /api/v1/templates/{id}/toggle", s.handleTemplatesToggle)
	mux.HandleFunc("POST /api/v1/templates/{id}/apply", s.handleTemplateApply)
	mux.HandleFunc("GET /api/v1/geodata/tags", s.handleGeodataTags)
	mux.HandleFunc("GET /api/v1/lan-devices", s.handleLANDevices)
	mux.HandleFunc("POST /api/v1/lan-devices/static", s.handleLANStatic)
	mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	mux.HandleFunc("PATCH /api/v1/settings", s.handleSettingsPatch)
	mux.HandleFunc("GET /api/v1/profile", s.handleProfilePreview)
	mux.HandleFunc("GET /api/v1/export", s.handleExport)
	mux.HandleFunc("POST /api/v1/import", s.handleImport)
	s.registerMonitor(mux)
}
