package cpa

import (
	"context"
	"fmt"

	"aiseat/internal/store"
)

// Expected reconciliation values (the prefix and proxy_url an account should
// have in CPA) are computed by the caller/orchestrator, not here:
//   - wantPrefix is account.Prefix() ("acct-<id>").
//   - wantProxy is the DECRYPTED egress proxy URL; the caller decrypts the
//     stored ciphertext via secure.Box before invoking these functions.
//
// These functions keep wantPrefix/wantProxy as explicit parameters both because
// the caller owns the expected values and for forward-compatibility (see the
// drift limitation documented on ReconcileAccount).

// ReconcileAccount fetches CPA's auth files, locates the one named
// account.CPAAuthName, and reports drift versus the expected prefix/proxy.
//
// It returns a human-readable drift string ("" when in sync, or when the file is
// present but nothing deeper can be compared) and never mutates CPA.
//
// LIMITATION: CPA's list endpoint (GET /v0/management/auth-files) does NOT
// return the per-file prefix or proxy_url, so we can only verify that a file
// with the expected name EXISTS. When it is missing, drift reports that. When it
// is present we return "" because prefix/proxy cannot be observed from the list
// response; the wantPrefix/wantProxy parameters are retained so a future CPA
// field exposing them enables a real value comparison here.
func ReconcileAccount(ctx context.Context, client *Client, account store.Account, wantPrefix, wantProxy string) (drift string, err error) {
	files, err := client.ListAuthFiles(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.Name == account.CPAAuthName {
			// File exists. We cannot see prefix/proxy from the list endpoint,
			// so treat it as in-sync (no observable drift).
			return "", nil
		}
	}
	return fmt.Sprintf("auth file %s missing in CPA", account.CPAAuthName), nil
}

// ApplyExpected pushes the expected prefix and proxy_url onto the account's CPA
// auth file via PATCH /v0/management/auth-files/fields.
func ApplyExpected(ctx context.Context, client *Client, account store.Account, wantPrefix, wantProxy string) error {
	return client.PatchFields(ctx, account.CPAAuthName, map[string]any{
		"prefix":    wantPrefix,
		"proxy_url": wantProxy,
	})
}
