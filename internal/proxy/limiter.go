// Package proxy provides concurrency primitives for the AI seat gateway,
// including a per-account in-flight limiter with FIFO fairness.
package proxy

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

// ErrQueueTimeout is returned by Acquire when maxWait elapses before a slot
// becomes available.
var ErrQueueTimeout = errors.New("queue timeout")

// waiter represents a goroutine blocked in Acquire waiting for a slot.
//
// ready is a buffered channel (capacity 1). A slot is handed off to the waiter
// by sending exactly once on ready while holding the Limiter mutex. Because the
// waiter is removed from its account queue before (or in the same critical
// section as) the send, and the channel is buffered, the send never blocks and
// never happens twice.
type waiter struct {
	ready chan struct{}
	elem  *list.Element // this waiter's element in acctState.queue, nil once dequeued
}

// acctState holds the concurrency bookkeeping for a single account. It is only
// ever read or mutated while Limiter.mu is held.
type acctState struct {
	inflight int        // slots currently held
	queue    *list.List // FIFO list of *waiter
}

// Limiter bounds the number of concurrent in-flight operations per account and
// serves queued waiters in strict arrival (FIFO) order.
//
// The zero value is not usable; construct one with NewLimiter.
type Limiter struct {
	mu    sync.Mutex
	accts map[int64]*acctState
}

// NewLimiter returns a ready-to-use Limiter.
func NewLimiter() *Limiter {
	return &Limiter{accts: make(map[int64]*acctState)}
}

// Acquire blocks until a slot is free for accountID, or ctx is cancelled, or
// maxWait elapses.
//
// capacity is the maximum in-flight for this account and is read fresh on every
// call. A capacity <= 0 is treated as 1 (slots are never unlimited or zero).
//
// On success it returns an idempotent release func, the time spent waiting, and
// a nil error. On timeout it returns ErrQueueTimeout; on ctx cancellation it
// returns ctx.Err(). A failed Acquire never consumes a slot.
func (l *Limiter) Acquire(ctx context.Context, accountID int64, capacity int, maxWait time.Duration) (release func(), waited time.Duration, err error) {
	if capacity <= 0 {
		capacity = 1
	}
	start := time.Now()

	l.mu.Lock()
	st := l.accts[accountID]
	if st == nil {
		st = &acctState{queue: list.New()}
		l.accts[accountID] = st
	}

	// Fast path: a slot is free and nobody is waiting ahead of us. Gating on an
	// empty queue preserves FIFO fairness (a new caller never jumps the line).
	if st.inflight < capacity && st.queue.Len() == 0 {
		st.inflight++
		l.mu.Unlock()
		return l.makeRelease(accountID, st), 0, nil
	}

	// Slow path: enqueue and wait for a hand-off.
	w := &waiter{ready: make(chan struct{}, 1)}
	w.elem = st.queue.PushBack(w)
	l.mu.Unlock()

	var timerC <-chan time.Time
	if maxWait > 0 {
		timer := time.NewTimer(maxWait)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case <-w.ready:
		// A slot was handed directly to us; inflight already accounts for it.
		return l.makeRelease(accountID, st), time.Since(start), nil
	case <-ctx.Done():
		return l.abandon(accountID, st, w, time.Since(start), ctx.Err())
	case <-timerC:
		return l.abandon(accountID, st, w, time.Since(start), ErrQueueTimeout)
	}
}

// InFlight returns the current in-flight and queued (waiting) counts for an
// account. Unknown accounts report 0, 0.
func (l *Limiter) InFlight(accountID int64) (inflight, queued int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.accts[accountID]
	if st == nil {
		return 0, 0
	}
	return st.inflight, st.queue.Len()
}

// makeRelease builds an idempotent release closure. sync.Once guarantees the
// underlying release runs at most once no matter how often (or from how many
// goroutines) the returned func is called, so a slot is never over-released.
func (l *Limiter) makeRelease(accountID int64, st *acctState) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.releaseLocked(accountID, st)
		})
	}
}

// releaseLocked releases one slot. l.mu must be held.
//
// If waiters exist, the slot is handed directly to the head waiter: inflight is
// left unchanged and the waiter wakes already holding the slot. This closes the
// race in which a fresh caller could otherwise steal the freed slot. Only when
// no waiters remain is inflight actually decremented (and empty account state
// reclaimed).
func (l *Limiter) releaseLocked(accountID int64, st *acctState) {
	if front := st.queue.Front(); front != nil {
		w := front.Value.(*waiter)
		st.queue.Remove(front)
		w.elem = nil
		w.ready <- struct{}{} // buffered cap 1, sent exactly once: never blocks
		return
	}
	if st.inflight > 0 {
		st.inflight--
	}
	if st.inflight == 0 && st.queue.Len() == 0 {
		delete(l.accts, accountID)
	}
}

// abandon removes a waiter that has timed out or been cancelled and returns the
// failure. l.mu is taken here so the decision is atomic with respect to any
// concurrent hand-off.
//
// The race: a releaser may hand us a slot at the exact instant our timer fires
// or ctx is cancelled. Under the lock the state is stable, so we simply check
// whether the hand-off already happened. If it did (ready holds a value, and we
// were already dequeued), we own a slot we no longer want, so we release it via
// releaseLocked to pass it to the next waiter or decrement inflight. Otherwise
// we are still queued and just remove ourselves, consuming no slot. Either way
// the slot is neither leaked nor double-counted.
func (l *Limiter) abandon(accountID int64, st *acctState, w *waiter, waited time.Duration, cause error) (func(), time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case <-w.ready:
		// We were granted a slot concurrently; release it so it isn't leaked.
		l.releaseLocked(accountID, st)
	default:
		// Still in the queue; drop ourselves without consuming a slot.
		if w.elem != nil {
			st.queue.Remove(w.elem)
			w.elem = nil
		}
		if st.inflight == 0 && st.queue.Len() == 0 {
			delete(l.accts, accountID)
		}
	}
	return nil, waited, cause
}
