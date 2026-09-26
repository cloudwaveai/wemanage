package web

import (
	"context"

	"aiseat/internal/store"
)

// Scope describes which employees a viewer may see.
type Scope struct {
	IsSuperAdmin bool
	Self         int64
	// visible is the set of employee IDs a department leader may view; nil for
	// super admins (all) and for plain employees (only Self).
	visible  map[int64]bool
	IsLeader bool
}

// CanViewEmployee reports whether the viewer may see the given employee.
func (s Scope) CanViewEmployee(id int64) bool {
	if s.IsSuperAdmin {
		return true
	}
	if id == s.Self {
		return true
	}
	return s.visible[id]
}

// VisibleEmployeeIDs returns the explicit visible set, or nil meaning "all"
// (super admin). Plain employees get just themselves.
func (s Scope) VisibleEmployeeIDs() []int64 {
	if s.IsSuperAdmin {
		return nil
	}
	ids := make([]int64, 0, len(s.visible)+1)
	seen := map[int64]bool{}
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	add(s.Self)
	for id := range s.visible {
		add(id)
	}
	return ids
}

// computeScope determines a viewer's visibility.
//
// A department leader sees employees in the departments they lead plus all
// descendant departments. Leadership is determined by the viewer's Feishu
// open_id appearing in a department's leader_open_ids.
func (s *Server) computeScope(ctx context.Context, viewer *store.Employee) (Scope, error) {
	sc := Scope{Self: viewer.ID, IsSuperAdmin: viewer.IsSuperAdmin}
	if viewer.IsSuperAdmin {
		return sc, nil
	}
	depts, err := s.deps.Store.ListDepartments(ctx)
	if err != nil {
		return sc, err
	}
	// Index by feishu id and parent relationships.
	byFeishu := make(map[string]store.Department, len(depts))
	children := make(map[string][]string)
	for _, d := range depts {
		byFeishu[d.FeishuDeptID] = d
		children[d.ParentFeishuID] = append(children[d.ParentFeishuID], d.FeishuDeptID)
	}
	// Departments the viewer leads.
	led := map[string]bool{}
	for _, d := range depts {
		for _, oid := range d.LeaderOpenIDs {
			if oid == viewer.FeishuOpenID {
				led[d.FeishuDeptID] = true
			}
		}
	}
	if len(led) == 0 {
		return sc, nil // plain employee: only self
	}
	sc.IsLeader = true
	// Expand to descendants (BFS).
	rowIDs := map[int64]bool{}
	queue := make([]string, 0, len(led))
	for f := range led {
		queue = append(queue, f)
	}
	visitedDept := map[string]bool{}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if visitedDept[f] {
			continue
		}
		visitedDept[f] = true
		if d, ok := byFeishu[f]; ok {
			rowIDs[d.ID] = true
		}
		queue = append(queue, children[f]...)
	}
	// Employees in those departments.
	sc.visible = map[int64]bool{}
	if len(rowIDs) > 0 {
		ids := make([]int64, 0, len(rowIDs))
		for id := range rowIDs {
			ids = append(ids, id)
		}
		rows, err := s.deps.Store.Pool.Query(ctx,
			`SELECT DISTINCT employee_id FROM employee_departments WHERE department_id = ANY($1)`, ids)
		if err != nil {
			return sc, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return sc, err
			}
			sc.visible[id] = true
		}
		if err := rows.Err(); err != nil {
			return sc, err
		}
	}
	return sc, nil
}
