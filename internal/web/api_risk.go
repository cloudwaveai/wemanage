package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
)

// strictMaxTrusted is the per-employee cap on trusted devices in strict mode.
const strictMaxTrusted = 2

// --- employee: my devices ---

func (s *Server) handleMyDevices(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	set := s.deps.Settings.Get(r.Context())
	devs, err := s.deps.Store.ListDevices(r.Context(), emp.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "devices query failed")
		return
	}
	nets, _ := s.deps.Store.ListNetworks(r.Context(), emp.ID, 20)
	maxTrusted := 0
	if set.RiskMode == settings.RiskStrict {
		maxTrusted = strictMaxTrusted
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":       nonNil(devs),
		"networks":      nonNil(nets),
		"confirm_hours": set.DeviceConfirmHours,
		"risk_mode":     set.RiskMode,
		"max_trusted":   maxTrusted,
	})
}

func (s *Server) ownDevice(r *http.Request) (store.Device, int, string) {
	emp := currentEmployee(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return store.Device{}, http.StatusBadRequest, "bad device id"
	}
	d, err := s.deps.Store.GetDevice(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.EmployeeID != emp.ID) {
		return store.Device{}, http.StatusNotFound, "device not found"
	}
	if err != nil {
		return store.Device{}, http.StatusInternalServerError, "device query failed"
	}
	return d, 0, ""
}

func (s *Server) handleConfirmDevice(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	d, code, msg := s.ownDevice(r)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	switch d.Status {
	case store.DeviceTrusted:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": d.Status})
		return
	case store.DeviceBlocked:
		writeErr(w, http.StatusForbidden, "这台设备已被拉黑，只能由管理员恢复")
		return
	}
	set := s.deps.Settings.Get(r.Context())
	if set.RiskMode == settings.RiskStrict {
		n, err := s.deps.Store.CountTrustedDevices(r.Context(), emp.ID)
		if err == nil && n >= strictMaxTrusted {
			writeErr(w, http.StatusConflict, fmt.Sprintf("最多只能确认 %d 台设备，请先让管理员移除旧设备", strictMaxTrusted))
			return
		}
	}
	if err := s.deps.Store.SetDeviceStatus(r.Context(), d.ID, store.DeviceTrusted, &emp.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot confirm device")
		return
	}
	if s.deps.Risk != nil {
		s.deps.Risk.DeviceChanged(emp.ID, d.ID, store.DeviceTrusted)
	}
	s.audit(r, "device_confirm", "device", fmt.Sprint(d.ID), map[string]any{"label": d.Label, "last_ip": d.LastIP})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": store.DeviceTrusted})
}

func (s *Server) handleDenyDevice(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	d, code, msg := s.ownDevice(r)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	ctx := r.Context()
	if err := s.deps.Store.SetDeviceStatus(ctx, d.ID, store.DeviceBlocked, nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot block device")
		return
	}
	// Someone else holds the key: revoke it so the employee reissues a fresh one.
	if err := s.deps.Store.RevokeKeys(ctx, emp.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot revoke key")
		return
	}
	if s.deps.Risk != nil {
		s.deps.Risk.DeviceDenied(ctx, *emp, d)
	} else {
		id := d.ID
		_, _ = s.deps.Store.AddRiskEvent(ctx, store.RiskEvent{EmployeeID: emp.ID, Rule: "device_denied", Severity: "high",
			DeviceID: &id, Detail: map[string]any{"label": d.Label, "last_ip": d.LastIP}})
	}
	s.audit(r, "device_deny", "device", fmt.Sprint(d.ID), map[string]any{"label": d.Label, "last_ip": d.LastIP, "key_revoked": true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": store.DeviceBlocked, "key_revoked": true})
}

// --- admin: risk center ---

func (s *Server) handleRiskEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.RiskEventFilter{Severity: q.Get("severity"), OpenOnly: q.Get("open") == "1"}
	if v := q.Get("employee_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.EmployeeID = &id
		}
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = v
	}
	evs, err := s.deps.Store.ListRiskEvents(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "risk events query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": nonNil(evs)})
}

func (s *Server) handleResolveRiskEvent(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.deps.Store.ResolveRiskEvent(r.Context(), id, &emp.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot resolve")
		return
	}
	s.audit(r, "risk_resolve", "risk_event", fmt.Sprint(id), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleEmployeeDevices(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ctx := r.Context()
	emp, err := s.deps.Store.GetEmployee(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "employee not found")
		return
	}
	devs, _ := s.deps.Store.ListDevices(ctx, id)
	nets, _ := s.deps.Store.ListNetworks(ctx, id, 50)
	evs, _ := s.deps.Store.ListRiskEvents(ctx, store.RiskEventFilter{EmployeeID: &id, Limit: 50})
	writeJSON(w, http.StatusOK, map[string]any{
		"employee": map[string]any{
			"id": emp.ID, "name": emp.Name, "email": emp.Email,
			"frozen": emp.FrozenAt != nil, "frozen_at": emp.FrozenAt, "frozen_reason": emp.FrozenReason,
			"remote_allowed": emp.RemoteAllowed,
		},
		"devices":  nonNil(devs),
		"networks": nonNil(nets),
		"events":   nonNil(evs),
	})
}

func (s *Server) handleAdminDevice(action string) http.HandlerFunc {
	status := map[string]string{
		"trust":   store.DeviceTrusted,
		"block":   store.DeviceBlocked,
		"unblock": store.DevicePending, // back to pending: the employee re-confirms
	}[action]
	return func(w http.ResponseWriter, r *http.Request) {
		admin := currentEmployee(r)
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad id")
			return
		}
		d, err := s.deps.Store.GetDevice(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "device not found")
			return
		}
		if action == "unblock" && d.Status != store.DeviceBlocked {
			writeErr(w, http.StatusConflict, "device is not blocked")
			return
		}
		if err := s.deps.Store.SetDeviceStatus(r.Context(), id, status, &admin.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot update device")
			return
		}
		if s.deps.Risk != nil {
			s.deps.Risk.DeviceChanged(d.EmployeeID, d.ID, status)
		}
		s.audit(r, "device_"+action, "device", fmt.Sprint(id),
			map[string]any{"employee_id": d.EmployeeID, "label": d.Label, "from": d.Status, "to": status})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
	}
}

func (s *Server) handleFreezeEmployee(freeze bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad id")
			return
		}
		ctx := r.Context()
		emp, err := s.deps.Store.GetEmployee(ctx, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "employee not found")
			return
		}
		if freeze {
			var body struct {
				Reason string `json:"reason"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Reason == "" {
				body.Reason = "管理员手动冻结"
			}
			if _, err := s.deps.Store.FreezeEmployee(ctx, id, body.Reason); err != nil {
				writeErr(w, http.StatusInternalServerError, "cannot freeze")
				return
			}
			s.audit(r, "employee_freeze", "employee", fmt.Sprint(id), map[string]any{"reason": body.Reason})
			if s.deps.Risk != nil {
				s.deps.Risk.Notify(ctx, emp.FeishuOpenID, "【AI 网关】你的 Key 已被管理员暂停使用。原因："+body.Reason)
			}
		} else {
			if err := s.deps.Store.UnfreezeEmployee(ctx, id); err != nil {
				writeErr(w, http.StatusInternalServerError, "cannot unfreeze")
				return
			}
			s.audit(r, "employee_unfreeze", "employee", fmt.Sprint(id), map[string]any{"was_reason": emp.FrozenReason})
			if s.deps.Risk != nil {
				s.deps.Risk.Unfrozen(id)
				s.deps.Risk.Notify(ctx, emp.FeishuOpenID, "【AI 网关】你的 Key 已恢复使用。如果之前有不认识的设备，建议到门户重新生成 Key。")
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "frozen": freeze})
	}
}

// handleRemoteAllowed toggles an employee's exemption from "office networks only".
func (s *Server) handleRemoteAllowed(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx := r.Context()
	if _, err := s.deps.Store.GetEmployee(ctx, id); err != nil {
		writeErr(w, http.StatusNotFound, "employee not found")
		return
	}
	if err := s.deps.Store.SetRemoteAllowed(ctx, id, body.Allowed); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot update")
		return
	}
	s.audit(r, "employee_remote_allowed", "employee", fmt.Sprint(id), map[string]any{"allowed": body.Allowed})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "remote_allowed": body.Allowed})
}

// nonNil turns a nil slice into an empty one so JSON renders [] not null.
func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
