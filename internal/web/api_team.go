package web

import (
	"net/http"
	"sort"

	"aiseat/internal/store"
)

// handleTeamUsage gives department leaders (and super admins) their team's
// total usage and, per member, only whether their account works (so they can
// help when someone is rate limited or has to sign in again). Per-person
// usage, activity times and conversations are deliberately not shown.
func (s *Server) handleTeamUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc, err := s.computeScope(ctx, currentEmployee(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "scope failed")
		return
	}
	if !sc.IsSuperAdmin && !sc.IsLeader {
		writeErr(w, http.StatusForbidden, "department leaders only")
		return
	}
	ids := sc.VisibleEmployeeIDs() // nil = everyone (super admin)
	from, to := usageRange(r)

	summary, err := s.deps.Store.Usage(ctx, from, to, ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	emps, err := s.deps.Store.ListEmployees(ctx, store.EmployeeFilter{IDs: ids, Status: "active"})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "employee query failed")
		return
	}
	accByID := map[int64]store.Account{}
	if accs, err := s.deps.Store.ListAccounts(ctx); err == nil {
		for _, a := range accs {
			accByID[a.ID] = a.Account
		}
	}

	members := make([]map[string]any, 0, len(emps))
	withAccount := 0
	for _, e := range emps {
		m := map[string]any{
			"employee_id": e.ID, "name": e.Name, "department_names": nonNil(e.DepartmentNames),
			"has_account": e.AccountID != nil,
		}
		if e.AccountID != nil {
			withAccount++
			if a, ok := accByID[*e.AccountID]; ok {
				m["account_status"] = accountStatus(a)
				m["rate_limited_until"] = a.RateLimitedUntil
			}
		}
		members = append(members, m)
	}
	sort.SliceStable(members, func(i, j int) bool {
		return members[i]["name"].(string) < members[j]["name"].(string)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary, "members": members, "with_account": withAccount,
	})
}
