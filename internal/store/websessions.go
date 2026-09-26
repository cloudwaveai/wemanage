package store

import (
	"context"
	"errors"
	"time"

	"aiseat/internal/secure"

	"github.com/jackc/pgx/v5"
)

// WebSession is a browser session for the admin/self-service UI.
type WebSession struct {
	ID         string
	EmployeeID int64
	CSRFToken  string
	ExpiresAt  time.Time
}

// CreateWebSession issues a new browser session for an employee.
func (s *Store) CreateWebSession(ctx context.Context, employeeID int64, ttl time.Duration) (id, csrf string, err error) {
	id = secure.RandomToken(32)
	csrf = secure.RandomToken(32)
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO web_sessions (id, employee_id, csrf_token, expires_at)
		VALUES ($1,$2,$3,$4)`, id, employeeID, csrf, time.Now().Add(ttl))
	if err != nil {
		return "", "", err
	}
	return id, csrf, nil
}

// GetWebSession returns a live session, or ErrNotFound if missing/expired.
func (s *Store) GetWebSession(ctx context.Context, id string) (WebSession, error) {
	var w WebSession
	err := s.Pool.QueryRow(ctx, `
		SELECT id, employee_id, csrf_token, expires_at FROM web_sessions
		WHERE id=$1 AND expires_at > now()`, id).
		Scan(&w.ID, &w.EmployeeID, &w.CSRFToken, &w.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebSession{}, ErrNotFound
	}
	return w, err
}

// DeleteWebSession removes a session (logout).
func (s *Store) DeleteWebSession(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM web_sessions WHERE id=$1`, id)
	return err
}

// DeleteExpiredWebSessions clears stale sessions.
func (s *Store) DeleteExpiredWebSessions(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM web_sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
