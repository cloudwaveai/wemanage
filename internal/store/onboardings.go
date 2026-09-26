package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Onboarding is an in-progress account OAuth/import session.
type Onboarding struct {
	State              string
	IPGroupID          int64
	OSProfile          DeviceProfile
	KnownAuthNames     []string
	CreatedBy          *int64
	CompletedAccountID *int64
}

// CreateOnboarding records a new account onboarding session.
func (s *Store) CreateOnboarding(ctx context.Context, o Onboarding) error {
	osp, _ := json.Marshal(o.OSProfile)
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO account_onboardings (state, ip_group_id, os_profile, known_auth_names, created_by)
		VALUES ($1,$2,$3,$4,$5)`,
		o.State, o.IPGroupID, osp, o.KnownAuthNames, o.CreatedBy)
	return err
}

// GetOnboarding returns an onboarding session by state, or ErrNotFound.
func (s *Store) GetOnboarding(ctx context.Context, state string) (Onboarding, error) {
	var o Onboarding
	var osp []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT state, ip_group_id, os_profile, known_auth_names, created_by, completed_account_id
		FROM account_onboardings WHERE state=$1`, state).
		Scan(&o.State, &o.IPGroupID, &osp, &o.KnownAuthNames, &o.CreatedBy, &o.CompletedAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Onboarding{}, ErrNotFound
	}
	if err != nil {
		return Onboarding{}, err
	}
	_ = json.Unmarshal(osp, &o.OSProfile)
	return o, nil
}

// CompleteOnboarding marks an onboarding as completed with the created account id.
func (s *Store) CompleteOnboarding(ctx context.Context, state string, accountID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE account_onboardings SET completed_account_id=$2 WHERE state=$1`, state, accountID)
	return err
}
