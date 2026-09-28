package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// EmployeeFilter narrows ListEmployees.
type EmployeeFilter struct {
	IDs    []int64 // if non-nil, restrict to these employee IDs
	Status string  // "" for any
}

// EmployeeRow is an employee joined with binding/key/department summary.
type EmployeeRow struct {
	Employee
	AccountID       *int64
	AccountEmail    string
	AccountStatus   string
	ActiveKeyPrefix string
	DepartmentNames []string
	OpenRiskEvents  int
}

func scanEmployee(r pgx.Row) (Employee, error) {
	var e Employee
	var openID *string
	err := r.Scan(&e.ID, &openID, &e.FeishuUnionID, &e.FeishuUserID, &e.Name,
		&e.Email, &e.AvatarURL, &e.Status, &e.IsSuperAdmin, &e.NoticeAckAt, &e.CreatedAt,
		&e.NoticeAckVersion, &e.FrozenAt, &e.FrozenReason, &e.RemoteAllowed)
	if openID != nil {
		e.FeishuOpenID = *openID
	}
	return e, err
}

const employeeCols = `id, feishu_open_id, feishu_union_id, feishu_user_id, name, email, avatar_url, status, is_super_admin, notice_ack_at, created_at, notice_ack_version, frozen_at, frozen_reason, remote_allowed`

// GetEmployee returns one employee by id.
func (s *Store) GetEmployee(ctx context.Context, id int64) (Employee, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+employeeCols+` FROM employees WHERE id=$1`, id)
	e, err := scanEmployee(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, ErrNotFound
	}
	return e, err
}

// GetEmployeeByOpenID returns one employee by Feishu open_id.
func (s *Store) GetEmployeeByOpenID(ctx context.Context, openID string) (Employee, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+employeeCols+` FROM employees WHERE feishu_open_id=$1`, openID)
	e, err := scanEmployee(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, ErrNotFound
	}
	return e, err
}

// UpsertEmployeeByOpenID inserts or updates an employee keyed on Feishu open_id.
// is_super_admin and notice_ack_at are preserved across updates.
func (s *Store) UpsertEmployeeByOpenID(ctx context.Context, e Employee) (Employee, error) {
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO employees (feishu_open_id, feishu_union_id, feishu_user_id, name, email, avatar_url, status)
		VALUES ($1,$2,$3,$4,$5,$6,COALESCE(NULLIF($7,''),'active'))
		ON CONFLICT (feishu_open_id) DO UPDATE SET
			feishu_union_id=EXCLUDED.feishu_union_id,
			feishu_user_id=EXCLUDED.feishu_user_id,
			name=EXCLUDED.name,
			email=EXCLUDED.email,
			avatar_url=EXCLUDED.avatar_url,
			status=EXCLUDED.status,
			updated_at=now()
		RETURNING `+employeeCols,
		e.FeishuOpenID, e.FeishuUnionID, e.FeishuUserID, e.Name, e.Email, e.AvatarURL, e.Status)
	return scanEmployee(row)
}

// EnsureSuperAdmin upserts a super-admin placeholder by open_id (used at bootstrap).
func (s *Store) EnsureSuperAdmin(ctx context.Context, openID, name string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO employees (feishu_open_id, name, is_super_admin)
		VALUES ($1, COALESCE(NULLIF($2,''),$1), TRUE)
		ON CONFLICT (feishu_open_id) DO UPDATE SET is_super_admin=TRUE, updated_at=now()`,
		openID, name)
	return err
}

// SetEmployeeStatus sets active/disabled.
func (s *Store) SetEmployeeStatus(ctx context.Context, id int64, status string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

// SetSuperAdmin toggles super-admin.
func (s *Store) SetSuperAdmin(ctx context.Context, id int64, v bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET is_super_admin=$2, updated_at=now() WHERE id=$1`, id, v)
	return err
}

// AckNotice records that the employee acknowledged the logging notice now.
func (s *Store) AckNotice(ctx context.Context, id int64, version int) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE employees SET notice_ack_at=now(), notice_ack_version=GREATEST(notice_ack_version,$2), updated_at=now()
		WHERE id=$1`, id, version)
	return err
}

// FreezeEmployee freezes an employee (all keys unusable, no self-service key
// reissue) until an admin unfreezes. A no-op if already frozen.
func (s *Store) FreezeEmployee(ctx context.Context, id int64, reason string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE employees SET frozen_at=now(), frozen_reason=$2, updated_at=now()
		WHERE id=$1 AND frozen_at IS NULL`, id, reason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetRemoteAllowed toggles the "may use the key outside office networks" exemption.
func (s *Store) SetRemoteAllowed(ctx context.Context, id int64, allowed bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET remote_allowed=$2, updated_at=now() WHERE id=$1`, id, allowed)
	return err
}

// UnfreezeEmployee clears a freeze.
func (s *Store) UnfreezeEmployee(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET frozen_at=NULL, frozen_reason='', updated_at=now() WHERE id=$1`, id)
	return err
}

// SuperAdminOpenIDs returns the Feishu open_ids of active super admins.
func (s *Store) SuperAdminOpenIDs(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT feishu_open_id FROM employees
		WHERE is_super_admin AND status='active' AND feishu_open_id IS NOT NULL AND feishu_open_id <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DisableEmployeesNotIn disables every active employee whose open_id is not in keep.
// Employees with a NULL open_id (manual seeds) are left untouched.
func (s *Store) DisableEmployeesNotIn(ctx context.Context, keepOpenIDs []string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE employees SET status='disabled', updated_at=now()
		WHERE status='active' AND feishu_open_id IS NOT NULL AND NOT (feishu_open_id = ANY($1))`,
		keepOpenIDs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListEmployees returns employees with binding/key/department summary.
func (s *Store) ListEmployees(ctx context.Context, f EmployeeFilter) ([]EmployeeRow, error) {
	sql := `
		SELECT e.id, e.feishu_open_id, e.feishu_union_id, e.feishu_user_id, e.name,
			e.email, e.avatar_url, e.status, e.is_super_admin, e.notice_ack_at, e.created_at,
			a.id, COALESCE(a.email,''), COALESCE(a.status,''),
			COALESCE(k.key_prefix,''),
			COALESCE(ARRAY_AGG(d.name) FILTER (WHERE d.name IS NOT NULL), '{}'),
			e.frozen_at, e.frozen_reason,
			(SELECT COUNT(*) FROM risk_events r WHERE r.employee_id = e.id AND r.resolved_at IS NULL),
			e.remote_allowed
		FROM employees e
		LEFT JOIN upstream_accounts a ON a.employee_id = e.id
		LEFT JOIN api_keys k ON k.employee_id = e.id AND k.revoked_at IS NULL
		LEFT JOIN employee_departments ed ON ed.employee_id = e.id
		LEFT JOIN departments d ON d.id = ed.department_id
		WHERE ($1::bigint[] IS NULL OR e.id = ANY($1))
		  AND ($2 = '' OR e.status = $2)
		GROUP BY e.id, a.id, a.email, a.status, k.key_prefix
		ORDER BY e.id`
	rows, err := s.Pool.Query(ctx, sql, nullableIDs(f.IDs), f.Status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmployeeRow
	for rows.Next() {
		var er EmployeeRow
		var openID *string
		err := rows.Scan(&er.ID, &openID, &er.FeishuUnionID, &er.FeishuUserID, &er.Name,
			&er.Email, &er.AvatarURL, &er.Status, &er.IsSuperAdmin, &er.NoticeAckAt, &er.CreatedAt,
			&er.AccountID, &er.AccountEmail, &er.AccountStatus, &er.ActiveKeyPrefix, &er.DepartmentNames,
			&er.FrozenAt, &er.FrozenReason, &er.OpenRiskEvents, &er.RemoteAllowed)
		if err != nil {
			return nil, err
		}
		if openID != nil {
			er.FeishuOpenID = *openID
		}
		out = append(out, er)
	}
	return out, rows.Err()
}

// nullableIDs returns nil for an empty slice so the SQL "$1 IS NULL" branch matches all.
func nullableIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	return ids
}
