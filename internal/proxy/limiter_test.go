package proxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// acqRes carries the result of an Acquire performed in a helper goroutine,
// passed back over a channel so the test goroutine reads it race-free.
type acqRes struct {
	release func()
	waited  time.Duration
	err     error
}

// waitForQueued blocks until the account reports exactly want queued waiters,
// making enqueue ordering deterministic without relying on fixed sleeps.
func waitForQueued(t *testing.T, l *Limiter, accountID int64, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, q := l.InFlight(accountID); q == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	_, q := l.InFlight(accountID)
	t.Fatalf("timed out waiting for queued==%d (have %d)", want, q)
}

// 1. Capacity 2: the 3rd concurrent Acquire must not return until one of the
// first two releases.
func TestCapacityBlocksBeyondLimit(t *testing.T) {
	l := NewLimiter()
	ctx := context.Background()
	const acct = int64(1)

	rel1, _, err := l.Acquire(ctx, acct, 2, time.Second)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	rel2, _, err := l.Acquire(ctx, acct, 2, time.Second)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}

	resCh := make(chan acqRes, 1)
	go func() {
		r, w, e := l.Acquire(ctx, acct, 2, time.Second)
		resCh <- acqRes{r, w, e}
	}()

	// The 3rd must stay blocked while both slots are held.
	select {
	case res := <-resCh:
		t.Fatalf("3rd Acquire returned early (err=%v) before any release", res.err)
	case <-time.After(50 * time.Millisecond):
	}

	if inf, q := l.InFlight(acct); inf != 2 || q != 1 {
		t.Fatalf("expected inflight=2 queued=1, got %d/%d", inf, q)
	}

	rel1() // free one slot -> hands off to the waiter
	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("3rd Acquire after release: %v", res.err)
		}
		res.release()
	case <-time.After(time.Second):
		t.Fatal("3rd Acquire did not return after a release")
	}

	rel2()
	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("expected 0/0 at end, got %d/%d", inf, q)
	}
}

// 2. FIFO fairness: with capacity 1, waiters enqueued as w1,w2,w3 complete in
// that exact order once the initial holder releases.
func TestFIFOOrder(t *testing.T) {
	l := NewLimiter()
	ctx := context.Background()
	const acct = int64(2)
	const n = 3

	hold, _, err := l.Acquire(ctx, acct, 1, time.Second)
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}

	completed := make(chan int, n)
	launch := func(id int) {
		r, _, e := l.Acquire(ctx, acct, 1, 2*time.Second)
		if e != nil {
			t.Errorf("waiter %d Acquire: %v", id, e)
			return
		}
		completed <- id
		r() // release so the next FIFO waiter is handed the slot
	}

	// Start waiters one at a time, waiting until each is enqueued before
	// starting the next so their arrival order is guaranteed 1,2,3.
	for i, id := range []int{1, 2, 3} {
		go launch(id)
		waitForQueued(t, l, acct, i+1)
	}

	hold() // start the FIFO hand-off chain

	var order []int
	for i := 0; i < n; i++ {
		select {
		case id := <-completed:
			order = append(order, id)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out; completed so far: %v", order)
		}
	}

	for i, id := range order {
		if id != i+1 {
			t.Fatalf("FIFO violated: completion order was %v", order)
		}
	}
	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("expected 0/0 at end, got %d/%d", inf, q)
	}
}

// 3. Timeout: capacity 1, hold the slot, a second Acquire with maxWait=50ms
// returns ErrQueueTimeout; releasing the holder then lets a fresh Acquire
// succeed and leaves no leak.
func TestAcquireTimeout(t *testing.T) {
	l := NewLimiter()
	ctx := context.Background()
	const acct = int64(3)

	hold, _, err := l.Acquire(ctx, acct, 1, time.Second)
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}

	_, waited, err := l.Acquire(ctx, acct, 1, 50*time.Millisecond)
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("expected ErrQueueTimeout, got %v", err)
	}
	if waited < 50*time.Millisecond {
		t.Fatalf("expected to wait at least 50ms, waited %v", waited)
	}

	// The timed-out waiter must have removed itself; the holder still holds.
	if inf, q := l.InFlight(acct); inf != 1 || q != 0 {
		t.Fatalf("after timeout expected inflight=1 queued=0, got %d/%d", inf, q)
	}

	hold()

	r, _, err := l.Acquire(ctx, acct, 1, time.Second)
	if err != nil {
		t.Fatalf("fresh acquire after release: %v", err)
	}
	r()
	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("leak detected: expected 0/0, got %d/%d", inf, q)
	}
}

// 4. Cancellation: capacity 1, hold the slot, a second Acquire whose ctx is
// cancelled returns ctx.Err() with no leak afterwards.
func TestAcquireCancellation(t *testing.T) {
	l := NewLimiter()
	const acct = int64(4)

	hold, _, err := l.Acquire(context.Background(), acct, 1, time.Second)
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	resCh := make(chan acqRes, 1)
	go func() {
		r, w, e := l.Acquire(ctx, acct, 1, time.Second)
		resCh <- acqRes{r, w, e}
	}()

	waitForQueued(t, l, acct, 1)
	cancel()

	res := <-resCh
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", res.err)
	}
	if inf, q := l.InFlight(acct); inf != 1 || q != 0 {
		t.Fatalf("after cancel expected inflight=1 queued=0, got %d/%d", inf, q)
	}

	hold()
	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("leak detected: expected 0/0, got %d/%d", inf, q)
	}
}

// 5. Race/stress: capacity 4, 100 goroutines each Acquire -> hold briefly ->
// release. The max concurrently-held count must never exceed capacity and the
// final in-flight count must be 0.
func TestStressNeverExceedsCapacity(t *testing.T) {
	l := NewLimiter()
	ctx := context.Background()
	const acct = int64(5)
	const capacity = 4
	const n = 100

	var cur, max int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, e := l.Acquire(ctx, acct, capacity, 5*time.Second)
			if e != nil {
				t.Errorf("acquire: %v", e)
				return
			}
			c := atomic.AddInt64(&cur, 1)
			for { // record the running maximum
				m := atomic.LoadInt64(&max)
				if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&cur, -1)
			r()
		}()
	}
	wg.Wait()

	if m := atomic.LoadInt64(&max); m > capacity {
		t.Fatalf("max concurrent in-flight %d exceeded capacity %d", m, capacity)
	}
	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("leak detected: expected 0/0, got %d/%d", inf, q)
	}
}

// 6. Idempotent release: calling release twice must not over-release, and the
// account must still enforce its full capacity afterwards (count never went
// negative).
func TestIdempotentRelease(t *testing.T) {
	l := NewLimiter()
	ctx := context.Background()
	const acct = int64(6)

	r, _, err := l.Acquire(ctx, acct, 2, time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	r()
	r() // second call must be a no-op

	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("after double release expected 0/0, got %d/%d", inf, q)
	}

	// Capacity 2 must still be honored exactly (had the count gone negative,
	// more than two acquisitions would fit before blocking).
	a, _, err := l.Acquire(ctx, acct, 2, time.Second)
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	b, _, err := l.Acquire(ctx, acct, 2, time.Second)
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	if _, _, err := l.Acquire(ctx, acct, 2, 30*time.Millisecond); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("expected capacity 2 to be enforced (timeout), got %v", err)
	}

	a()
	b()
	if inf, q := l.InFlight(acct); inf != 0 || q != 0 {
		t.Fatalf("leak detected: expected 0/0, got %d/%d", inf, q)
	}
}
