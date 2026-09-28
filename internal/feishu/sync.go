package feishu

import (
	"context"
	"errors"

	"aiseat/internal/store"
)

// ErrEmptyDirectory is returned when Feishu lists no users at all; nobody is
// disabled in that case.
var ErrEmptyDirectory = errors.New("feishu sync: directory returned 0 users; check the app's contact data scope (通讯录权限范围) and contact permissions")

// Run mirrors the Feishu contact directory (the app's contact scope, whole
// tenant or partial) into the store: it upserts every department and user, links each user to its departments, disables employees
// whose open_id no longer appears in the feed, and writes a "feishu_sync"
// audit record with the counts.
func Run(ctx context.Context, st *store.Store, c *Client) (deptCount, userCount int, err error) {
	depts, users, err := c.Directory(ctx)
	if err != nil {
		return 0, 0, err
	}
	rowIDByFeishu := make(map[string]int64, len(depts))
	for _, d := range depts {
		// leader_open_ids is NOT NULL in the schema; a nil slice encodes as SQL
		// NULL, so normalize leaderless departments to an empty array.
		leaders := d.LeaderOpenIDs
		if leaders == nil {
			leaders = []string{}
		}
		rowID, uerr := st.UpsertDepartment(ctx, store.Department{
			FeishuDeptID:   d.ID,
			ParentFeishuID: d.ParentID,
			Name:           d.Name,
			LeaderOpenIDs:  leaders,
		})
		if uerr != nil {
			return 0, 0, uerr
		}
		rowIDByFeishu[d.ID] = rowID
		deptCount++
	}

	// An empty feed almost always means the app's contact scope or
	// permissions are not set up; never treat it as "everyone left".
	if len(users) == 0 {
		return deptCount, 0, ErrEmptyDirectory
	}
	seen := make([]string, 0, len(users))
	for _, u := range users {
		status := "active"
		if !u.Active {
			status = "disabled"
		}
		emp, uerr := st.UpsertEmployeeByOpenID(ctx, store.Employee{
			FeishuOpenID:  u.OpenID,
			FeishuUnionID: u.UnionID,
			FeishuUserID:  u.UserID,
			Name:          u.Name,
			Email:         u.Email,
			AvatarURL:     u.AvatarURL,
			Status:        status,
		})
		if uerr != nil {
			return 0, 0, uerr
		}
		var deptIDs []int64
		for _, fid := range u.DeptFeishuIDs {
			if rowID, ok := rowIDByFeishu[fid]; ok {
				deptIDs = append(deptIDs, rowID)
			}
		}
		if serr := st.SetEmployeeDepartments(ctx, emp.ID, deptIDs); serr != nil {
			return 0, 0, serr
		}
		seen = append(seen, u.OpenID)
		userCount++
	}

	disabled, err := st.DisableEmployeesNotIn(ctx, seen)
	if err != nil {
		return 0, 0, err
	}

	if aerr := st.Audit(ctx, store.AuditEntry{
		Action: "feishu_sync",
		Detail: map[string]any{
			"departments": deptCount,
			"users":       userCount,
			"disabled":    disabled,
		},
	}); aerr != nil {
		return 0, 0, aerr
	}
	return deptCount, userCount, nil
}
