package wemanage

import (
	"net/http"
	"time"
)

type orgDepartment struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	ParentID      *int64 `json:"parent_id"`
	ParentOutside bool   `json:"parent_outside"`
}
type orgMember struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	DepartmentIDs []int64 `json:"department_ids"`
}

// organizationDirectory reads the existing Feishu directory mirror. It never
// exposes private employees, leadership IDs, e-mail addresses or service keys.
func (s *Service) organizationDirectory(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `select d.id,d.name,p.id,(d.parent_feishu_dept_id not in ('','0') and p.id is null) from departments d left join departments p on p.feishu_dept_id=d.parent_feishu_dept_id order by d.name,d.id`)
	if err != nil {
		fail(w, 500, "读取飞书部门失败")
		return
	}
	departments := []orgDepartment{}
	for rows.Next() {
		var d orgDepartment
		if rows.Scan(&d.ID, &d.Name, &d.ParentID, &d.ParentOutside) != nil {
			rows.Close()
			fail(w, 500, "读取飞书部门失败")
			return
		}
		departments = append(departments, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "读取飞书部门失败")
		return
	}
	rows, err = s.Store.Pool.Query(r.Context(), `select e.id,e.name,coalesce(array_agg(ed.department_id order by ed.department_id) filter(where ed.department_id is not null),'{}'::bigint[]) from employees e left join employee_departments ed on ed.employee_id=e.id where e.status='active' and not e.is_service and e.private_work_since is null and e.name<>'林伊默' and e.feishu_open_id is not null and e.feishu_open_id<>'' group by e.id,e.name order by e.name,e.id`)
	if err != nil {
		fail(w, 500, "读取飞书成员失败")
		return
	}
	members := []orgMember{}
	for rows.Next() {
		var m orgMember
		if rows.Scan(&m.ID, &m.Name, &m.DepartmentIDs) != nil {
			rows.Close()
			fail(w, 500, "读取飞书成员失败")
			return
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "读取飞书成员失败")
		return
	}
	var syncedAt *time.Time
	if err = s.Store.Pool.QueryRow(r.Context(), `select max(at) from audit_logs where action='feishu_sync'`).Scan(&syncedAt); err != nil {
		fail(w, 500, "读取同步时间失败")
		return
	}
	reply(w, 200, map[string]any{"departments": departments, "members": members, "synced_at": syncedAt, "source": "飞书通讯录同步镜像", "note": "仅限飞书应用可见范围；部门关系按最近同步快照展示，历史用量按当前部门归属，非历史组织快照。"})
}
