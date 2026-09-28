package store

import (
	"context"
	"time"
)

// UpsertObservedClient records a seen client UA, adding c.Hits uses (default 1).
func (s *Store) UpsertObservedClient(ctx context.Context, c ObservedClient) error {
	hits := c.Hits
	if hits <= 0 {
		hits = 1
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO observed_clients (user_agent, originator, version, first_seen, last_seen, hits)
		VALUES ($1,$2,$3,now(),now(),$4)
		ON CONFLICT (user_agent) DO UPDATE SET
			originator=EXCLUDED.originator,
			version=EXCLUDED.version,
			last_seen=now(),
			hits=observed_clients.hits+$4`,
		c.UserAgent, c.Originator, c.Version, hits)
	return err
}

// TopObservedClients returns the most frequent clients seen since a time.
func (s *Store) TopObservedClients(ctx context.Context, since time.Time, limit int) ([]ObservedClient, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT user_agent, originator, version, first_seen, last_seen, hits
		FROM observed_clients WHERE last_seen >= $1
		ORDER BY hits DESC LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObservedClient
	for rows.Next() {
		var c ObservedClient
		if err := rows.Scan(&c.UserAgent, &c.Originator, &c.Version, &c.FirstSeen, &c.LastSeen, &c.Hits); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
