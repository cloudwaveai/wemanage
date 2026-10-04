package proxy

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"aiseat/internal/store"
)

// maxStale is how long a cached principal may keep serving while the database
// is unreachable (e.g. a cross-border link to a domestic DB is down). During
// that window freezes/revocations made elsewhere take effect late; beyond it
// requests fail with 503 rather than guessing.
const maxStale = 10 * time.Minute

// principalCache caches key->principal lookups. After ttl an entry is still
// served immediately while it is refreshed in the background
// (stale-while-revalidate), so requests do not wait on a possibly remote
// database; a change such as a freeze or revocation therefore lands within
// about ttl plus one refresh. Entries older than maxStale (the refresh keeps
// failing) are looked up synchronously. On database errors (not "no such
// key") the last known principal keeps serving up to maxStale.
type principalCache struct {
	st         *store.Store
	ttl        time.Duration
	mu         sync.Mutex
	m          map[string]cachedPrincipal
	refreshing map[string]bool
}

type cachedPrincipal struct {
	p  store.KeyPrincipal
	at time.Time
}

func newPrincipalCache(st *store.Store, ttl time.Duration) *principalCache {
	return &principalCache{st: st, ttl: ttl, m: make(map[string]cachedPrincipal), refreshing: map[string]bool{}}
}

// Get resolves a key hash to its principal.
func (c *principalCache) Get(ctx context.Context, hash []byte) (store.KeyPrincipal, error) {
	k := hex.EncodeToString(hash)
	c.mu.Lock()
	if e, ok := c.m[k]; ok {
		age := time.Since(e.at)
		if age < c.ttl {
			c.mu.Unlock()
			return e.p, nil
		}
		if age < maxStale {
			if !c.refreshing[k] {
				c.refreshing[k] = true
				go c.refresh(k, hash)
			}
			c.mu.Unlock()
			return e.p, nil
		}
	}
	c.mu.Unlock()

	p, err := c.st.LookupKey(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		c.mu.Lock()
		delete(c.m, k)
		c.mu.Unlock()
		return store.KeyPrincipal{}, err
	}
	if err != nil {
		c.mu.Lock()
		e, ok := c.m[k]
		c.mu.Unlock()
		if ok && time.Since(e.at) < maxStale {
			return e.p, nil
		}
		return store.KeyPrincipal{}, err
	}
	c.put(k, p)
	return p, nil
}

func (c *principalCache) refresh(k string, hash []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := c.st.LookupKey(ctx, hash)
	c.mu.Lock()
	delete(c.refreshing, k)
	c.mu.Unlock()
	switch {
	case errors.Is(err, store.ErrNotFound):
		c.mu.Lock()
		delete(c.m, k)
		c.mu.Unlock()
	case err == nil:
		c.put(k, p)
	}
	// other errors: keep serving the stale entry (up to maxStale)
}

func (c *principalCache) put(k string, p store.KeyPrincipal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = cachedPrincipal{p: p, at: time.Now()}
	// opportunistic cleanup to bound memory
	if len(c.m) > 4096 {
		for kk, e := range c.m {
			if time.Since(e.at) > maxStale {
				delete(c.m, kk)
			}
		}
	}
}

// forgetAccount drops entries bound to an account whose status this process
// just changed (rate limited, needs login), so the next request sees it.
func (c *principalCache) forgetAccount(accountID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.m {
		if _, ok := e.p.Account(accountID); ok {
			delete(c.m, k)
		}
	}
}
