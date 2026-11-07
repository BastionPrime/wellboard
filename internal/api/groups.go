// Package api contains groups handlers.
package api

import (
	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"net/http"
	"strings"
)

type groupIn struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

func (s *Server) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, map[string]any{"groups": st.Groups})
}

func (s *Server) handleGroupGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	for i := range st.Groups {
		if st.Groups[i].ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, st.Groups[i])
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
}

func validateGroupIn(st *model.State, in groupIn, selfID string) (model.Group, error) {
	if in.Name == "" {
		return model.Group{}, httpErr(http.StatusBadRequest, "name is required")
	}
	if strings.HasPrefix(in.Name, generator.ServiceGroupPrefix) {
		return model.Group{}, httpErr(http.StatusBadRequest,
			"group names starting with %q are reserved", generator.ServiceGroupPrefix)
	}
	switch model.GroupType(in.Type) {
	case model.GroupSelect, model.GroupURLTest, model.GroupFallback, model.GroupLoadBalance:
	default:
		return model.Group{}, httpErr(http.StatusBadRequest,
			"type must be one of select, url-test, fallback, load-balance")
	}
	if len(in.Members) == 0 {
		return model.Group{}, httpErr(http.StatusBadRequest, "at least one member is required")
	}
	for _, m := range in.Members {
		if m == selfID {
			return model.Group{}, httpErr(http.StatusBadRequest, "group cannot contain itself")
		}
		if !existsServer(st, m) && !existsGroup(st, m) {
			return model.Group{}, httpErr(http.StatusBadRequest,
				"member %q is neither a known server nor group id", m)
		}
	}
	return model.Group{Name: in.Name, Type: model.GroupType(in.Type), Members: in.Members}, nil
}

func (s *Server) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var in groupIn
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
	g, err := validateGroupIn(st, in, "")
	if err != nil {
		writeError(w, err)
		return
	}
	g.ID = newID("grp", st)
	st.Groups = append(st.Groups, g)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("group %s created", g.ID)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleGroupPatch(w http.ResponseWriter, r *http.Request) {
	var in groupIn
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
	idx := -1
	for i := range st.Groups {
		if st.Groups[i].ID == r.PathValue("id") {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}
	if in.Name == "" {
		writeError(w, httpErr(http.StatusBadRequest, "name is required"))
		return
	}
	if strings.HasPrefix(in.Name, generator.ServiceGroupPrefix) {
		writeError(w, httpErr(http.StatusBadRequest, "reserved prefix"))
		return
	}
	st.Groups[idx].Name = in.Name
	st.Groups[idx].Type = model.GroupType(in.Type)
	st.Groups[idx].Members = in.Members
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st.Groups[idx])
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	id := r.PathValue("id")
	idx := -1
	for i := range st.Groups {
		if st.Groups[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}
	if affected := routesTargeting(st, id); len(affected) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "group is a route target; delete or reassign those routes first",
			"routes": affected,
		})
		return
	}
	// Remove from other groups' members.
	for gi := range st.Groups {
		if st.Groups[gi].ID != id {
			st.Groups[gi].Members = removeString(st.Groups[gi].Members, id)
		}
	}
	st.Groups = append(st.Groups[:idx], st.Groups[idx+1:]...)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ----------------------------------------------------------------------------
// Routes (FR-4)
// ----------------------------------------------------------------------------

// routeIn is the create/update payload for a route.
