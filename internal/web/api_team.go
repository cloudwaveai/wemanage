package web

import (
	"net/http"
	"sort"
	"time"

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
	members := make([]map[string]any, 0, len(emps))
	withAccount := 0
	for _, e := range emps {
		m := map[string]any{
			"employee_id": e.ID, "name": e.Name, "department_names": nonNil(e.DepartmentNames),
			"has_account": len(e.Accounts) > 0, "accounts": len(e.Accounts),
		}
		if len(e.Accounts) > 0 {
			withAccount++
			status, until := bestAccountStatus(e.Accounts)
			m["account_status"] = status
			m["rate_limited_until"] = until
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

// bestAccountStatus is how an employee's accounts stand together: usable if
// any is; otherwise the status of the one closest to usable (the soonest
// reset among rate-limited ones).
func bestAccountStatus(accs []store.EmployeeAccount) (string, *time.Time) {
	rank := map[string]int{"active": 0, "rate_limited": 1, "needs_login": 2, "disabled": 3}
	best, bestRank := "", 99
	var until *time.Time
	for _, a := range accs {
		st := accountStatus(store.Account{Status: a.Status, NeedsLogin: a.NeedsLogin, RateLimitedUntil: a.RateLimitedUntil})
		r := rank[st]
		if r < bestRank || (st == "rate_limited" && r == bestRank && a.RateLimitedUntil.Before(*until)) {
			best, bestRank = st, r
			until = nil
			if st == "rate_limited" {
				until = a.RateLimitedUntil
			}
		}
	}
	return best, until
}
