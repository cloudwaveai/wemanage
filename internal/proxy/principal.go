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

// principalCache caches key->principal lookups for a short TTL to absorb bursts.
// The TTL is deliberately short so that account status changes (rate limit,
// needs-login) propagate quickly. On database errors (not "no such key") it
// serves the last known principal for up to maxStale.
type principalCache struct {
	st  *store.Store
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]cachedPrincipal
}

type cachedPrincipal struct {
	p  store.KeyPrincipal
	at time.Time
}

func newPrincipalCache(st *store.Store, ttl time.Duration) *principalCache {
	return &principalCache{st: st, ttl: ttl, m: make(map[string]cachedPrincipal)}
}

// Get resolves a key hash to its principal, using the cache when fresh.
func (c *principalCache) Get(ctx context.Context, hash []byte) (store.KeyPrincipal, error) {
	k := hex.EncodeToString(hash)
	c.mu.Lock()
	if e, ok := c.m[k]; ok && time.Since(e.at) < c.ttl {
		c.mu.Unlock()
		return e.p, nil
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
	c.mu.Lock()
	c.m[k] = cachedPrincipal{p: p, at: time.Now()}
	// opportunistic cleanup to bound memory
	if len(c.m) > 4096 {
		for kk, e := range c.m {
			if time.Since(e.at) > maxStale {
				delete(c.m, kk)
			}
		}
	}
	c.mu.Unlock()
	return p, nil
}
