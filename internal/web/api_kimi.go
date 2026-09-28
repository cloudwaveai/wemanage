package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/store"
)

// Kimi Code accounts (used for trial runs before ChatGPT) are added with a
// device-code login: the admin opens the returned URL, approves, then calls
// complete; CPA saves the credential on its own.

func (s *Server) handleKimiStart(w http.ResponseWriter, r *http.Request) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	var body struct {
		IPGroupID int64               `json:"ip_group_id"`
		OS        store.DeviceProfile `json:"os_profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPGroupID == 0 {
		writeErr(w, http.StatusBadRequest, "ip_group_id required")
		return
	}
	known, err := s.deps.CPA.ListAuthFiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot list CPA auth files: "+err.Error())
		return
	}
	authURL, state, userCode, err := s.deps.CPA.RequestKimiAuthURL(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot start Kimi login: "+err.Error())
		return
	}
	names := make([]string, 0, len(known))
	for _, f := range known {
		names = append(names, f.Name)
	}
	emp := currentEmployee(r)
	if err := s.deps.Store.CreateOnboarding(r.Context(), store.Onboarding{
		State: state, IPGroupID: body.IPGroupID, OSProfile: body.OS, KnownAuthNames: names, CreatedBy: &emp.ID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot record onboarding")
		return
	}
	s.audit(r, "account_kimi_start", "ip_group", fmt.Sprint(body.IPGroupID), nil)
	writeJSON(w, http.StatusOK, map[string]any{"auth_url": authURL, "state": state, "user_code": userCode})
}

func (s *Server) handleKimiComplete(w http.ResponseWriter, r *http.Request) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.State == "" {
		writeErr(w, http.StatusBadRequest, "state required")
		return
	}
	ob, err := s.deps.Store.GetOnboarding(r.Context(), body.State)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unknown onboarding state")
		return
	}
	// The device flow finishes in CPA's background; wait briefly for it.
	status := "pending"
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if status, _ = s.deps.CPA.AuthStatus(r.Context(), body.State); status != "pending" {
			break
		}
	}
	switch status {
	case "pending":
		writeErr(w, http.StatusConflict, "尚未完成授权：请在打开的页面里确认授权后再点完成")
		return
	case "error":
		writeErr(w, http.StatusBadGateway, "Kimi 授权失败或已过期，请重新开始")
		return
	}
	files, err := s.deps.CPA.ListAuthFiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot list auth files")
		return
	}
	known := map[string]bool{}
	for _, n := range ob.KnownAuthNames {
		known[n] = true
	}
	var newFile *cpa.AuthFile
	for i := range files {
		if files[i].Type == store.ProviderKimi && !known[files[i].Name] {
			newFile = &files[i]
			break
		}
	}
	if newFile == nil {
		writeErr(w, http.StatusBadGateway, "no new Kimi account detected")
		return
	}
	acc, err := s.finishAccount(r, ob, newFile.Name, newFile.Email, store.ProviderKimi)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.CompleteOnboarding(r.Context(), body.State, acc.ID)
	s.audit(r, "account_added", "account", fmt.Sprint(acc.ID), map[string]any{"via": "kimi", "cpa_name": newFile.Name})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID, "email": acc.Email})
}
