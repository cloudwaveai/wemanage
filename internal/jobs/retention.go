package jobs

import (
	"context"
	"time"

	"aiseat/internal/store"
)

// Retention deletes conversation content (blobs + searchable text) older than
// retentionDays, marks the affected requests as purged, and clears expired
// web sessions and OAuth states. Request metadata, usage and reports are kept.
func Retention(ctx context.Context, st *store.Store, retentionDays int, now time.Time) error {
	if retentionDays <= 0 {
		retentionDays = 180
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)

	if _, err := st.PurgeExpiredBlobs(ctx, cutoff); err != nil {
		return err
	}
	if _, err := st.DeleteExpiredTexts(ctx, cutoff); err != nil {
		return err
	}
	if _, err := st.MarkPurgedRequests(ctx, cutoff); err != nil {
		return err
	}
	if _, err := st.DeleteExpiredWebSessions(ctx); err != nil {
		return err
	}
	// Expire stale OAuth states (older than 1 hour).
	_, err := st.Pool.Exec(ctx, `DELETE FROM oauth_states WHERE created_at < $1`, now.Add(-time.Hour))
	return err
}
