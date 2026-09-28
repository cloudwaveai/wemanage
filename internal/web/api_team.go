package web

import (
	"net/http"
	"sort"

	"aiseat/internal/store"
)

// handleTeamUsage gives department leaders (and super admins) per-member
// usage and each member's account status and latest quota. Read-only: the
// actions stay with super admins.
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
	per, err := s.deps.Store.UsageByEmployee(ctx, from, to, ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	emps, err := s.deps.Store.ListEmployees(ctx, store.EmployeeFilter{IDs: ids, Status: "active"})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "employee query failed")
		return
	}
	var accIDs []int64
	for _, e := range emps {
		if e.AccountID != nil {
			accIDs = append(accIDs, *e.AccountID)
		}
	}
	quotas, _ := s.deps.Store.LatestQuotas(ctx, accIDs)
	statusByAcc := map[int64]string{}
	if accs, err := s.deps.Store.ListAccounts(ctx); err == nil {
		for _, a := range accs {
			statusByAcc[a.ID] = accountStatus(a.Account)
		}
	}

	members := make([]map[string]any, 0, len(emps))
	for _, e := range emps {
		u := per[e.ID]
		m := map[string]any{
			"employee_id": e.ID, "name": e.Name, "department_names": nonNil(e.DepartmentNames),
			"requests": u.Requests, "tokens_input": u.TokensInput, "tokens_output": u.TokensOutput,
			"last_active": u.LastActive, "frozen": e.FrozenAt != nil,
			"has_account": e.AccountID != nil,
		}
		if e.AccountID != nil {
			m["account_status"] = statusByAcc[*e.AccountID]
			if q, ok := quotas[*e.AccountID]; ok {
				m["quota"] = map[string]any{
					"at": q.At, "primary_used_pct": q.PrimaryUsedPct, "primary_reset_at": q.PrimaryResetAt,
					"secondary_used_pct": q.SecondaryUsedPct, "secondary_reset_at": q.SecondaryResetAt,
				}
			}
		}
		members = append(members, m)
	}
	sort.SliceStable(members, func(i, j int) bool {
		return members[i]["requests"].(int64) > members[j]["requests"].(int64)
	})
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "members": members})
}
