package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const accountCols = `id, cpa_auth_name, email, plan_type, owner_type, ip_group_id, status, needs_login, rate_limited_until, max_concurrency, device_profile, cpa_drift, created_at, provider, allowed_models, allow_third_party`

// accountDests are the scan destinations for accountCols; finish must run
// after the scan.
func accountDests(a *Account) (dests []any, finish func()) {
	var dp []byte
	return []any{&a.ID, &a.CPAAuthName, &a.Email, &a.PlanType, &a.OwnerType,
			&a.IPGroupID, &a.Status, &a.NeedsLogin, &a.RateLimitedUntil, &a.MaxConcurrency, &dp, &a.CPADrift, &a.CreatedAt, &a.Provider,
			&a.AllowedModels, &a.AllowThirdParty},
		func() {
			if len(dp) > 0 {
				_ = json.Unmarshal(dp, &a.DeviceProfile)
			}
		}
}

func scanAccount(r pgx.Row) (Account, error) {
	var a Account
	dests, finish := accountDests(&a)
	if err := r.Scan(dests...); err != nil {
		return Account{}, err
	}
	finish()
	return a, nil
}

// AccountMember is an employee sharing an account.
type AccountMember struct {
	EmployeeID int64  `json:"id"`
	Name       string `json:"name"`
}

// AccountRow is an account with its members and ip-group name.
type AccountRow struct {
	Account
	Members     []AccountMember
	IPGroupName string
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
	if a.Provider == "" {
		a.Provider = ProviderCodex
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO upstream_accounts (cpa_auth_name, email, plan_type, owner_type, ip_group_id, status, device_profile, provider)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+accountCols,
		a.CPAAuthName, a.Email, a.PlanType, a.OwnerType, a.IPGroupID, a.Status, dp, a.Provider)
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
			COALESCE((SELECT json_agg(json_build_object('id', e.id, 'name', e.name) ORDER BY m.created_at, e.id)
				FROM account_members m JOIN employees e ON e.id = m.employee_id WHERE m.account_id = a.id), '[]'),
			COALESCE(g.name,'')
		FROM upstream_accounts a
		LEFT JOIN ip_groups g ON g.id = a.ip_group_id
		ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountRow
	for rows.Next() {
		var ar AccountRow
		var members []byte
		dests, finish := accountDests(&ar.Account)
		if err := rows.Scan(append(dests, &members, &ar.IPGroupName)...); err != nil {
			return nil, err
		}
		finish()
		_ = json.Unmarshal(members, &ar.Members)
		out = append(out, ar)
	}
	return out, rows.Err()
}

// AccountsOf returns an employee's accounts, oldest binding first.
func (s *Store) AccountsOf(ctx context.Context, employeeID int64) ([]Account, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+prefixCols("a", accountCols)+`
		FROM upstream_accounts a JOIN account_members m ON m.account_id = a.id
		WHERE m.employee_id = $1
		ORDER BY m.created_at, a.id`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		dests, finish := accountDests(&a)
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}
		finish()
		out = append(out, a)
	}
	return out, rows.Err()
}

// AccountMemberIDs returns the employees sharing an account.
func (s *Store) AccountMemberIDs(ctx context.Context, accountID int64) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT employee_id FROM account_members WHERE account_id=$1 ORDER BY created_at, employee_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BindAccount adds an employee to an account. Binding an existing member is a
// no-op; an account that already has maxMembers employees gives ErrAccountFull.
func (s *Store) BindAccount(ctx context.Context, accountID, employeeID int64, maxMembers int) error {
	return s.tx(ctx, func(q queryer) error {
		var id int64
		if err := q.QueryRow(ctx, `SELECT id FROM upstream_accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var member bool
		var n int
		if err := q.QueryRow(ctx, `SELECT COALESCE(bool_or(employee_id=$2), false), COUNT(*) FROM account_members WHERE account_id=$1`,
			accountID, employeeID).Scan(&member, &n); err != nil {
			return err
		}
		if member {
			return nil
		}
		if maxMembers > 0 && n >= maxMembers {
			return ErrAccountFull
		}
		_, err := q.Exec(ctx, `INSERT INTO account_members (account_id, employee_id) VALUES ($1,$2)`, accountID, employeeID)
		return err
	})
}

// UnbindAccount removes an employee from an account.
func (s *Store) UnbindAccount(ctx context.Context, accountID, employeeID int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM account_members WHERE account_id=$1 AND employee_id=$2`, accountID, employeeID)
	return err
}

// SetAccountModels sets the models an account may be used with; nil allows all.
func (s *Store) SetAccountModels(ctx context.Context, id int64, models []string) error {
	var v any
	if models != nil {
		v = models
	}
	_, err := s.Pool.Exec(ctx, `UPDATE upstream_accounts SET allowed_models=$2, updated_at=now() WHERE id=$1`, id, v)
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
	// AllowThirdParty lets clients other than Codex use the account.
	AllowThirdParty *bool
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
	if p.AllowThirdParty != nil {
		add("allow_third_party=", *p.AllowThirdParty)
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
