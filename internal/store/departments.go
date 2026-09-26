package store

import "context"

// Department is a synced Feishu department.
type Department struct {
	ID             int64
	FeishuDeptID   string
	ParentFeishuID string
	Name           string
	LeaderOpenIDs  []string
}

// UpsertDepartment inserts or updates a department by Feishu dept id, returning its row id.
func (s *Store) UpsertDepartment(ctx context.Context, d Department) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO departments (feishu_dept_id, parent_feishu_dept_id, name, leader_open_ids)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (feishu_dept_id) DO UPDATE SET
			parent_feishu_dept_id=EXCLUDED.parent_feishu_dept_id,
			name=EXCLUDED.name,
			leader_open_ids=EXCLUDED.leader_open_ids,
			updated_at=now()
		RETURNING id`,
		d.FeishuDeptID, d.ParentFeishuID, d.Name, d.LeaderOpenIDs).Scan(&id)
	return id, err
}

// ListDepartments returns all departments.
func (s *Store) ListDepartments(ctx context.Context) ([]Department, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, feishu_dept_id, parent_feishu_dept_id, name, leader_open_ids
		FROM departments ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Department
	for rows.Next() {
		var d Department
		if err := rows.Scan(&d.ID, &d.FeishuDeptID, &d.ParentFeishuID, &d.Name, &d.LeaderOpenIDs); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetEmployeeDepartments replaces an employee's department memberships (by dept row ids).
func (s *Store) SetEmployeeDepartments(ctx context.Context, employeeID int64, deptIDs []int64) error {
	return s.tx(ctx, func(q queryer) error {
		if _, err := q.Exec(ctx, `DELETE FROM employee_departments WHERE employee_id=$1`, employeeID); err != nil {
			return err
		}
		for _, did := range deptIDs {
			if _, err := q.Exec(ctx,
				`INSERT INTO employee_departments (employee_id, department_id) VALUES ($1,$2)
				 ON CONFLICT DO NOTHING`, employeeID, did); err != nil {
				return err
			}
		}
		return nil
	})
}

// DepartmentIDByFeishuID returns the row id for a Feishu dept id, or ErrNotFound.
func (s *Store) DepartmentIDByFeishuID(ctx context.Context, feishuDeptID string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `SELECT id FROM departments WHERE feishu_dept_id=$1`, feishuDeptID).Scan(&id)
	if err != nil {
		return 0, ErrNotFound
	}
	return id, nil
}
