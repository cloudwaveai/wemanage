package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type APIChannel struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Protocol         string    `json:"protocol"`
	BaseURL          string    `json:"base_url"`
	Active           bool      `json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	CredentialCipher []byte    `json:"-"`
}

func (s *Store) CreateAPIChannel(ctx context.Context, c APIChannel) (APIChannel, error) {
	err := s.Pool.QueryRow(ctx, `INSERT INTO api_channels(name,protocol,base_url,credential_cipher) VALUES ($1,$2,$3,$4) RETURNING id,active,created_at`, c.Name, c.Protocol, c.BaseURL, c.CredentialCipher).Scan(&c.ID, &c.Active, &c.CreatedAt)
	c.CredentialCipher = nil
	return c, err
}
func (s *Store) GetAPIChannel(ctx context.Context, id int64) (APIChannel, error) {
	var c APIChannel
	err := s.Pool.QueryRow(ctx, `SELECT id,name,protocol,base_url,credential_cipher,active,created_at FROM api_channels WHERE id=$1`, id).Scan(&c.ID, &c.Name, &c.Protocol, &c.BaseURL, &c.CredentialCipher, &c.Active, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}
func (s *Store) ListAPIChannels(ctx context.Context) ([]APIChannel, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,name,protocol,base_url,active,created_at FROM api_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIChannel{}
	for rows.Next() {
		var c APIChannel
		if err := rows.Scan(&c.ID, &c.Name, &c.Protocol, &c.BaseURL, &c.Active, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) SetAPIChannelActive(ctx context.Context, id int64, active bool) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE api_channels SET active=$2 WHERE id=$1`, id, active)
	return tag.RowsAffected() == 1, err
}

func (s *Store) RotateAPIChannelCredential(ctx context.Context, id int64, cipher []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE api_channels SET credential_cipher=$2 WHERE id=$1`, id, cipher)
	return tag.RowsAffected() == 1, err
}
