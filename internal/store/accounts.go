package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const accountCols = `id, cpa_auth_name, email, plan_type, owner_type, employee_id, ip_group_id, status, needs_login, rate_limited_until, max_concurrency, device_profile, cpa_drift, created_at`

func scanAccount(r pgx.Row) (Account, error) {
	var a Account
	var dp []byte
	err := r.Scan(&a.ID, &a.CPAAuthName, &a.Email, &a.PlanType, &a.OwnerType, &a.EmployeeID,
		&a.IPGroupID, &a.Status, &a.NeedsLogin, &a.RateLimitedUntil, &a.MaxConcurrency, &dp, &a.CPADrift, &a.CreatedAt)
	if err != nil {
		return Account{}, err
	}
	if len(dp) > 0 {
		_ = json.Unmarshal(dp, &a.DeviceProfile)
	}
	return a, nil
}

// AccountRow is an account joined with employee/ip-group display names.
type AccountRow struct {
	Account
	EmployeeName string
	IPGroupName  string
}

// CreateAccount inserts a new upstream account.
func (s *Store) CreateAccount(ctx context.Context, a Account) (Account, error) {
	dp, _ := json.Marshal(a.DeviceProfile)
	if a.OwnerType == "" {
		a.OwnerType = "company"
	}
	if a.Status == "" {
		a.Status = "active"
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO upstream_accounts (cpa_auth_name, email, plan_type, owner_type, ip_group_id, status, device_profile)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING `+accountCols,
		a.CPAAuthName, a.Email, a.PlanType, a.OwnerType, a.IPGroupID, a.Status, dp)
	return scanAccount(row)
}

// GetAccount returns one account by id.
func (s *Store) GetAccount(ctx context.Context, id int64) (Account, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+accountCols+` FROM upstream_accounts WHERE id=$1`, id)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// GetAccountByCPAName returns one account by CPA auth-file name.
func (s *Store) GetAccountByCPAName(ctx context.Context, name string) (Account, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+accountCols+` FROM upstream_accounts WHERE cpa_auth_name=$1`, name)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// ListAccounts returns all accounts with display joins.
func (s *Store) ListAccounts(ctx context.Context) ([]AccountRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+prefixCols("a", accountCols)+`,
			COALESCE(e.name,''), COALESCE(g.name,'')
		FROM upstream_accounts a
		LEFT JOIN employees e ON e.id = a.employee_id
		LEFT JOIN ip_groups g ON g.id = a.ip_group_id
		ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountRow
	for rows.Next() {
		var ar AccountRow
		var dp []byte
		err := rows.Scan(&ar.ID, &ar.CPAAuthName, &ar.Email, &ar.PlanType, &ar.OwnerType, &ar.EmployeeID,
			&ar.IPGroupID, &ar.Status, &ar.NeedsLogin, &ar.RateLimitedUntil, &ar.MaxConcurrency, &dp, &ar.CPADrift, &ar.CreatedAt,
			&ar.EmployeeName, &ar.IPGroupName)
		if err != nil {
			return nil, err
		}
		if len(dp) > 0 {
			_ = json.Unmarshal(dp, &ar.DeviceProfile)
		}
		out = append(out, ar)
	}
	return out, rows.Err()
}

// BindAccount binds an account to an employee (1:1). Fails with ErrConflict if
// the employee already has an account or the account is already bound to someone else.
func (s *Store) BindAccount(ctx context.Context, accountID, employeeID int64) error {
	return s.tx(ctx, func(q queryer) error {
		var existingEmp *int64
		if err := q.QueryRow(ctx, `SELECT employee_id FROM upstream_accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&existingEmp); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if existingEmp != nil && *existingEmp != employeeID {
			return ErrConflict
		}
		var otherAcct int64
		err := q.QueryRow(ctx, `SELECT id FROM upstream_accounts WHERE employee_id=$1 AND id<>$2`, employeeID, accountID).Scan(&otherAcct)
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = q.Exec(ctx, `UPDATE upstream_accounts SET employee_id=$2, updated_at=now() WHERE id=$1`, accountID, employeeID)
		return err
	})
}

// UnbindAccount removes the employee binding.
func (s *Store) UnbindAccount(ctx context.Context, accountID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET employee_id=NULL, updated_at=now() WHERE id=$1`, accountID)
	return err
}

// AccountPatch carries optional account field updates.
type AccountPatch struct {
	Status         *string
	IPGroupID      *int64
	MaxConcurrency *int // set to a pointer-to-negative to clear (NULL)
	Email          *string
	PlanType       *string
	DeviceProfile  *DeviceProfile
}

// UpdateAccount applies a partial update.
func (s *Store) UpdateAccount(ctx context.Context, id int64, p AccountPatch) error {
	set := "updated_at=now()"
	args := []any{id}
	n := 1
	add := func(frag string, v any) {
		n++
		set += ", " + frag + "$" + itoa(int64(n))
		args = append(args, v)
	}
	if p.Status != nil {
		add("status=", *p.Status)
	}
	if p.IPGroupID != nil {
		add("ip_group_id=", *p.IPGroupID)
	}
	if p.Email != nil {
		add("email=", *p.Email)
	}
	if p.PlanType != nil {
		add("plan_type=", *p.PlanType)
	}
	if p.MaxConcurrency != nil {
		if *p.MaxConcurrency <= 0 {
			set += ", max_concurrency=NULL"
		} else {
			add("max_concurrency=", *p.MaxConcurrency)
		}
	}
	if p.DeviceProfile != nil {
		dp, _ := json.Marshal(*p.DeviceProfile)
		add("device_profile=", dp)
	}
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET `+set+` WHERE id=$1`, args...)
	return err
}

// SetNeedsLogin flags/clears the re-login requirement.
func (s *Store) SetNeedsLogin(ctx context.Context, id int64, v bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET needs_login=$2, updated_at=now() WHERE id=$1`, id, v)
	return err
}

// SetRateLimitedUntil sets or clears the rate-limit deadline.
func (s *Store) SetRateLimitedUntil(ctx context.Context, id int64, until *time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET rate_limited_until=$2, updated_at=now() WHERE id=$1`, id, until)
	return err
}

// SetCPADrift records the latest CPA reconciliation drift (empty = in sync).
func (s *Store) SetCPADrift(ctx context.Context, id int64, drift string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET cpa_drift=$2, updated_at=now() WHERE id=$1`, id, drift)
	return err
}

// CountAccountsInGroup returns how many accounts use an IP group.
func (s *Store) CountAccountsInGroup(ctx context.Context, groupID int64) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_accounts WHERE ip_group_id=$1`, groupID).Scan(&n)
	return n, err
}

// prefixCols qualifies a comma-separated column list with a table alias.
func prefixCols(alias, cols string) string {
	out := ""
	start := 0
	for i := 0; i <= len(cols); i++ {
		if i == len(cols) || cols[i] == ',' {
			field := cols[start:i]
			// trim spaces
			for len(field) > 0 && field[0] == ' ' {
				field = field[1:]
			}
			for len(field) > 0 && field[len(field)-1] == ' ' {
				field = field[:len(field)-1]
			}
			if out != "" {
				out += ", "
			}
			out += alias + "." + field
			start = i + 1
		}
	}
	return out
}
