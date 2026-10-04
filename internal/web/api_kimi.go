package web

import (
	"encoding/json"
	"net/http"

	"aiseat/internal/store"
)

// handleKimiStart and handleKimiComplete are the Kimi Code device login
// (login.go): the admin opens the returned URL and approves; CPA saves the
// credential on its own.
func (s *Server) handleKimiStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IPGroupID int64               `json:"ip_group_id"`
		OS        store.DeviceProfile `json:"os_profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPGroupID == 0 {
		writeErr(w, http.StatusBadRequest, "ip_group_id required")
		return
	}
	s.startLogin(w, r, store.ProviderKimi, body.IPGroupID, body.OS)
}

func (s *Server) handleKimiComplete(w http.ResponseWriter, r *http.Request) {
	s.handleLoginComplete(w, r)
}
