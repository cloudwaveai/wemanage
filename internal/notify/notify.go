// Package notify pushes account-health alerts to super administrators over an
// injectable message sender, marking signals notified and suppressing repeats.
package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"aiseat/internal/store"
)

// dedupWindow is how long a repeat alert for the same (account,kind) is muted.
const dedupWindow = 24 * time.Hour

// Sender delivers a text message to a Feishu open_id. *feishu.Client satisfies
// this interface.
type Sender interface {
	SendText(ctx context.Context, openID, text string) error
}

// Notifier fans account alerts out to a fixed set of super-admin open_ids and
// de-duplicates repeats per (account,kind) within a 24h in-memory window.
type Notifier struct {
	st                *store.Store
	sender            Sender
	superAdminOpenIDs []string

	mu    sync.Mutex
	dedup map[string]time.Time
}

// NewNotifier constructs a Notifier.
func NewNotifier(st *store.Store, sender Sender, superAdminOpenIDs []string) *Notifier {
	return &Notifier{
		st:                st,
		sender:            sender,
		superAdminOpenIDs: superAdminOpenIDs,
		dedup:             make(map[string]time.Time),
	}
}

// AccountAlert marks the signal notified, then — unless an identical alert was
// sent for the same (account,kind) within the last 24h — sends a text alert to
// every super admin. It is safe for concurrent use.
func (n *Notifier) AccountAlert(ctx context.Context, signalID, accountID int64, kind, detail string) error {
	if err := n.st.MarkNotified(ctx, signalID); err != nil {
		return err
	}

	key := fmt.Sprintf("%d|%s", accountID, kind)
	now := time.Now()
	n.mu.Lock()
	last, seen := n.dedup[key]
	suppress := seen && now.Sub(last) < dedupWindow
	if !suppress {
		n.dedup[key] = now
	}
	n.mu.Unlock()
	if suppress {
		return nil
	}

	text := fmt.Sprintf("[account %d] %s: %s", accountID, kind, detail)
	for _, openID := range n.superAdminOpenIDs {
		if err := n.sender.SendText(ctx, openID, text); err != nil {
			return err
		}
	}
	return nil
}
