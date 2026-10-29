package jobs

import (
	"context"
	"time"

	"aiseat/internal/store"
)

// blobRetentionSlack keeps blobs slightly longer than their requests' window.
const blobRetentionSlack = 48 * time.Hour

// Retention always clears expired sessions and OAuth states. When explicitly
// enabled it also purges old content and marks the affected requests.
func Retention(ctx context.Context, st *store.Store, retentionDays int, purgeContent bool, now time.Time) error {
	if retentionDays <= 0 {
		retentionDays = 180
	}
	if purgeContent {
		cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)

		// Blobs get two extra days: the log recorder refreshes a blob's
		// last_referenced_at at most once a day, so a blob still used by a request
		// just inside the window may carry a timestamp up to a day older.
		if _, err := st.PurgeExpiredBlobs(ctx, cutoff.Add(-blobRetentionSlack)); err != nil {
			return err
		}
		if _, err := st.DeleteExpiredTexts(ctx, cutoff); err != nil {
			return err
		}
		if err := st.DeleteExpiredWorkDigests(ctx, cutoff); err != nil {
			return err
		}
		if err := st.DeleteExpiredAdminBriefings(ctx, cutoff); err != nil {
			return err
		}
		if _, err := st.MarkPurgedRequests(ctx, cutoff); err != nil {
			return err
		}
	}
	if _, err := st.DeleteExpiredWebSessions(ctx); err != nil {
		return err
	}
	// Expire stale OAuth states (older than 1 hour).
	_, err := st.Pool.Exec(ctx, `DELETE FROM oauth_states WHERE created_at < $1`, now.Add(-time.Hour))
	return err
}
