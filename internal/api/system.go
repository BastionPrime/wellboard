// Package api contains system handlers.
package api

import (
	"errors"
	"github.com/wellboard/wellboard/internal/geodata"
	"github.com/wellboard/wellboard/internal/model"
	"net/http"
)

func (s *Server) handleGeodataTags(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "geosite" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind must be \"geosite\""})
		return
	}
	paths := s.geositeDatPaths
	if paths == nil {
		paths = defaultGeositeDatPaths()
	}
	tags := geodata.KnownGeositeTags(paths)
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// defaultGeositeDatPaths probes the local geosite.dat copies: the
// nikki/mihomo run dir first, then the WellBoard state-dir cache.
// Missing files are skipped by KnownGeositeTags.
func defaultGeositeDatPaths() []string {
	return []string{
		"/etc/nikki/run/geosite.dat",
		"/etc/wellboard/geosite.dat",
	}
}

// ----------------------------------------------------------------------------
// LAN devices (FR-4.4)
// ----------------------------------------------------------------------------

// LANReader abstracts the lease source list.
type LANReader interface {
	Devices() ([]model.LANDevice, error)
}

// SetStater applies a static lease (UCI on the router; dev stub).
type SetStater interface {
	SetStatic(dev model.LANDevice) error
}

// LAN wires the reader + static-lease writer into the API.
func (s *Server) SetLAN(reader LANReader, static SetStater) {
	s.lanReader = reader
	s.lanStatic = static
}

func (s *Server) handleLANDevices(w http.ResponseWriter, r *http.Request) {
	if s.lanReader == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LAN device listing unavailable"})
		return
	}
	devs, err := s.lanReader.Devices()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// staticIn requests a static lease for one device.
type staticIn struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

func (s *Server) handleLANStatic(w http.ResponseWriter, r *http.Request) {
	if s.lanStatic == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "static leases unavailable"})
		return
	}
	var in staticIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	dev := model.LANDevice{MAC: in.MAC, IP: in.IP, Hostname: in.Hostname}
	err := s.lanStatic.SetStatic(dev)
	if err != nil && errors.Is(err, errDevStubSentinel) {
		// Dev mode: documented stub — success-but-simulated.
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "simulated", "detail": err.Error(),
		})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

// ----------------------------------------------------------------------------
// Settings (FR-9.1)
// ----------------------------------------------------------------------------

// settingsPatch is the mutable settings subset.
