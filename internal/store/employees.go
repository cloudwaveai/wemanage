package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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
	Accounts        []EmployeeAccount
	ActiveKeyPrefix string
	DepartmentNames []string
	OpenRiskEvents  int
}

// EmployeeAccount summarizes one of an employee's accounts.
type EmployeeAccount struct {
	ID               int64      `json:"id"`
	Email            string     `json:"email"`
	Provider         string     `json:"provider"`
	PlanType         string     `json:"plan_type"`
	Status           string     `json:"status"`
	NeedsLogin       bool       `json:"needs_login"`
	RateLimitedUntil *time.Time `json:"rate_limited_until"`
}

func scanEmployee(r pgx.Row) (Employee, error) {
	var e Employee
	var openID *string
	err := r.Scan(&e.ID, &openID, &e.FeishuUnionID, &e.FeishuUserID, &e.Name,
		&e.Email, &e.AvatarURL, &e.Status, &e.IsSuperAdmin, &e.NoticeAckAt, &e.CreatedAt,
		&e.NoticeAckVersion, &e.FrozenAt, &e.FrozenReason, &e.RemoteAllowed, &e.WeeklyReportOff, &e.QuotaAlert, &e.IsService)
	if openID != nil {
		e.FeishuOpenID = *openID
	}
	return e, err
}

const employeeCols = `id, feishu_open_id, feishu_union_id, feishu_user_id, name, email, avatar_url, status, is_super_admin, notice_ack_at, created_at, notice_ack_version, frozen_at, frozen_reason, remote_allowed, weekly_report_off, quota_alert, is_service`

// CreateServiceAccount adds a service account: active, usable from any
// network, without weekly report or quota reminders.
func (s *Store) CreateServiceAccount(ctx context.Context, name string) (Employee, error) {
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO employees (name, status, is_service, remote_allowed, weekly_report_off, quota_alert)
		VALUES ($1, 'active', true, true, true, false)
		RETURNING `+employeeCols, name)
	return scanEmployee(row)
}

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
// SetWeeklyReportOff turns the employee's weekly AI recap off or back on.
func (s *Store) SetWeeklyReportOff(ctx context.Context, id int64, off bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET weekly_report_off=$2, updated_at=now() WHERE id=$1`, id, off)
	return err
}

// SetQuotaAlert turns the employee's 80% quota reminder on or off.
func (s *Store) SetQuotaAlert(ctx context.Context, id int64, on bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET quota_alert=$2, updated_at=now() WHERE id=$1`, id, on)
	return err
}

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
			COALESCE((SELECT json_agg(json_build_object('id', a.id, 'email', a.email, 'provider', a.provider,
					'plan_type', a.plan_type, 'status', a.status, 'needs_login', a.needs_login,
					'rate_limited_until', a.rate_limited_until) ORDER BY m.created_at, a.id)
				FROM account_members m JOIN upstream_accounts a ON a.id = m.account_id WHERE m.employee_id = e.id), '[]'),
			COALESCE((SELECT key_prefix FROM api_keys WHERE employee_id=e.id AND revoked_at IS NULL ORDER BY created_at DESC, id DESC LIMIT 1),''),
			COALESCE(ARRAY_AGG(d.name) FILTER (WHERE d.name IS NOT NULL), '{}'),
			e.frozen_at, e.frozen_reason,
			(SELECT COUNT(*) FROM risk_events r WHERE r.employee_id = e.id AND r.resolved_at IS NULL),
			e.remote_allowed, e.is_service
		FROM employees e
				LEFT JOIN employee_departments ed ON ed.employee_id = e.id
		LEFT JOIN departments d ON d.id = ed.department_id
		WHERE ($1::bigint[] IS NULL OR e.id = ANY($1))
		  AND ($2 = '' OR e.status = $2)
		GROUP BY e.id
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
		var accounts []byte
		err := rows.Scan(&er.ID, &openID, &er.FeishuUnionID, &er.FeishuUserID, &er.Name,
			&er.Email, &er.AvatarURL, &er.Status, &er.IsSuperAdmin, &er.NoticeAckAt, &er.CreatedAt,
			&accounts, &er.ActiveKeyPrefix, &er.DepartmentNames,
			&er.FrozenAt, &er.FrozenReason, &er.OpenRiskEvents, &er.RemoteAllowed, &er.IsService)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(accounts, &er.Accounts); err != nil {
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

// PrivateWork reports whether the employee's work is private to its owner.
func (s *Store) PrivateWork(ctx context.Context, id int64) (bool, error) {
	var private bool
	err := s.Pool.QueryRow(ctx, `SELECT (private_work_since IS NOT NULL OR name='林伊默') FROM employees WHERE id=$1`, id).Scan(&private)
	return private, err
}

// PrivacyCutoff marks the earliest time shared aggregates can have been created
// after the protected employee was excluded. Existing aggregates stay stored.
func (s *Store) PrivacyCutoff(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT max(private_work_since) FROM employees WHERE private_work_since IS NOT NULL`).Scan(&t)
	return t, err
}

// SharedWorkEmployeeIDs returns the non-private identities for shared reports.
func (s *Store) SharedWorkEmployeeIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM employees WHERE private_work_since IS NULL AND name <> '林伊默'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
