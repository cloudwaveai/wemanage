// Package recorder asynchronously persists request/response logs: it splits
// bodies into deduplicated, redacted segments, prefix-compresses each session's
// hash list, extracts searchable text, and spools to disk when the DB is down.
//
// The database may be far away (e.g. across a border, ~200ms per round trip),
// so the recorder avoids per-request database work:
//
//   - preparation (split, redact, hash, prefix-encode) runs on parallel
//     workers, sharded by session so a session's records stay in order;
//   - each session's previous-request state is kept in memory (read from the
//     database only the first time a session is seen by this process);
//   - which blobs are already stored is remembered in memory, so known blobs
//     are neither re-sent nor re-checked (only their retention timestamp is
//     bumped, at most once a day);
//   - a single writer commits everything prepared in the last second (or up
//     to maxBatch records) in ONE round trip / transaction.
//
// It assumes a single gateway process writes the logs.
package recorder

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/jackc/pgx/v5/pgconn"
)

// Record is a single request/response to be logged.
type Record struct {
	RequestID       [16]byte
	StartedAt       time.Time
	EmployeeID      int64
	AccountID       *int64
	APIKeyID        *int64
	SessionKey      string
	ClientKind      string
	UserAgent       string
	Endpoint        string // "responses" or "chat"
	Stream          bool
	ModelRequested  string
	ReasoningEffort string
	StatusCode      int
	ErrorType       string
	ErrorBody       string
	QueuedMS        int
	DurationMS      int
	ReqBody         []byte
	Outcome         capture.Outcome
	ClientIP        string
	DeviceID        *int64
}

// Tunables.
const (
	workers  = 8              // parallel preparation workers
	maxBatch = 200            // records per database commit at most
	linger   = time.Second    // how long the writer gathers records before committing
	bumpSkew = 24 * time.Hour // a blob's retention timestamp is refreshed at most this often
	idleSess = 2 * time.Hour  // in-memory session state is dropped after this idle time
	memoTTL  = 48 * time.Hour // remembered blob hashes expire after this
	sweepGap = 10 * time.Minute
)

// sessState is a session's latest (possibly not yet committed) request state.
type sessState struct {
	id         int64
	lastReq    *[16]byte
	full       [][32]byte
	depth      int
	inputCount int
	used       time.Time
}

// prepared is a record ready to be committed.
type prepared struct {
	rec      Record
	epoch    uint64
	row      store.RequestRow
	texts    []store.TextRow
	newBlobs []store.Blob
	bumps    []store.BlobRef
	adv      store.SessionAdvance
	hashes   [][32]byte // every blob hash the record references
}

// Recorder buffers Records and persists them in batches.
type Recorder struct {
	st       *store.Store
	set      *settings.Service
	spoolDir string
	log      *slog.Logger

	shards []chan Record
	out    chan *prepared
	kick   chan struct{}
	wg     sync.WaitGroup // records not yet committed or spooled
	mu     sync.Mutex     // guards spool file writes
	ctx    context.Context
	replay atomic.Bool // an early spool replay is scheduled

	stMu     sync.Mutex
	epoch    uint64                 // bumped when a commit fails; older prepared records are re-done
	sessions map[string]*sessState  // session key -> latest state
	stored   map[[32]byte]time.Time // blob hash -> latest committed last_referenced_at
	uploaded map[[32]byte]time.Time // external backend: bytes uploaded by this process (wall time)
}

// New creates a Recorder whose queues hold about `queue` records in total.
func New(st *store.Store, set *settings.Service, spoolDir string, log *slog.Logger, queue int) *Recorder {
	if queue <= 0 {
		queue = 1024
	}
	if log == nil {
		log = slog.Default()
	}
	per := queue / workers
	if per < 1 {
		per = 1
	}
	shards := make([]chan Record, workers)
	for i := range shards {
		shards[i] = make(chan Record, per)
	}
	return &Recorder{
		st: st, set: set, spoolDir: spoolDir, log: log,
		shards: shards, out: make(chan *prepared, maxBatch), kick: make(chan struct{}, 1),
		sessions: map[string]*sessState{}, stored: map[[32]byte]time.Time{}, uploaded: map[[32]byte]time.Time{},
	}
}

func sessKey(rec Record) string { return strconv.FormatInt(rec.EmployeeID, 10) + "|" + rec.SessionKey }

// shardFor keeps every record of one session on one worker.
func (r *Recorder) shardFor(rec Record) chan Record {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessKey(rec)))
	return r.shards[h.Sum32()%uint32(len(r.shards))]
}

// Start replays any spooled records and launches the workers and the writer.
// When ctx is cancelled, records not yet committed are spooled to disk.
func (r *Recorder) Start(ctx context.Context) {
	r.ctx = ctx
	_ = os.MkdirAll(r.spoolDir, 0o700)
	for _, ch := range r.shards {
		go r.prepareLoop(ctx, ch)
	}
	go r.writeLoop(ctx)
	// Records spooled before a restart are queued now (written asynchronously;
	// Flush waits for them).
	r.replaySpool(ctx)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.replaySpool(ctx)
			}
		}
	}()
}

// Submit enqueues a record without blocking; on a full queue it spools to disk.
func (r *Recorder) Submit(rec Record) {
	r.wg.Add(1)
	select {
	case r.shardFor(rec) <- rec:
	default:
		r.wg.Done()
		r.spool(rec)
	}
}

// Flush blocks until every submitted record is committed or spooled, asking
// the writer to commit immediately instead of waiting out its linger time.
func (r *Recorder) Flush() { r.FlushTimeout(0) }

// FlushTimeout is Flush with an upper bound (0 = no bound). It reports whether
// everything was flushed in time.
func (r *Recorder) FlushTimeout(d time.Duration) bool {
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	var deadline <-chan time.Time
	if d > 0 {
		deadline = time.After(d)
	}
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			return true
		case <-deadline:
			return false
		case <-t.C:
			select {
			case r.kick <- struct{}{}:
			default:
			}
		}
	}
}

func (r *Recorder) prepareLoop(ctx context.Context, ch chan Record) {
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case rec := <-ch:
					r.spool(rec)
					r.wg.Done()
				default:
					return
				}
			}
		case rec := <-ch:
			p, err := r.prepare(ctx, rec)
			if err != nil {
				r.log.Warn("recorder: prepare failed, spooling", "err", err, "request_id", uuidString(rec.RequestID))
				r.spool(rec)
				r.wg.Done()
				continue
			}
			select {
			case r.out <- p:
			case <-ctx.Done():
				r.spool(rec)
				r.wg.Done()
			}
		}
	}
}

// session returns the in-memory state for rec's session, loading it from the
// database on first sight (the only database read on the logging path).
func (r *Recorder) session(ctx context.Context, rec Record) (sessState, uint64, error) {
	key := sessKey(rec)
	r.stMu.Lock()
	if st, ok := r.sessions[key]; ok {
		st.used = time.Now()
		cp, ep := *st, r.epoch
		r.stMu.Unlock()
		return cp, ep, nil
	}
	ep := r.epoch
	r.stMu.Unlock()

	prev, err := r.st.BeginSession(ctx, rec.EmployeeID, rec.AccountID, rec.SessionKey, rec.ClientKind, rec.StartedAt)
	if err != nil {
		return sessState{}, 0, err
	}
	st := sessState{id: prev.ID, lastReq: prev.PrevRequestID, full: prev.PrevFull,
		depth: prev.PrevChainDepth, inputCount: prev.PrevInputCount, used: time.Now()}
	r.stMu.Lock()
	if r.epoch == ep {
		cp := st
		r.sessions[key] = &cp
	}
	r.stMu.Unlock()
	return st, ep, nil
}

// prepare turns a record into rows, touching the database only for a session
// seen for the first time, and (external backend) uploading new blob bytes.
func (r *Recorder) prepare(ctx context.Context, rec Record) (*prepared, error) {
	set := r.set.Get(ctx)
	red, err := capture.NewRedactor(set.RedactionRules)
	if err != nil {
		return nil, err
	}
	reqSegs, inputStart, err := capture.SplitRequest(rec.Endpoint, rec.ReqBody, red)
	if err != nil {
		return nil, err
	}
	respSegs := capture.SplitResponse(rec.Outcome, red)

	sess, ep, err := r.session(ctx, rec)
	if err != nil {
		return nil, err
	}

	reqHashes := make([][32]byte, len(reqSegs))
	inputCount := 0
	for i, s := range reqSegs {
		reqHashes[i] = s.Hash
		if s.Kind == "input" {
			inputCount++
		}
	}
	var prevState *capture.PrevState
	if sess.lastReq != nil {
		prevState = &capture.PrevState{ID: capture.UUID(*sess.lastReq), Full: sess.full, ChainDepth: sess.depth}
	}
	enc := capture.Encode(reqHashes, prevState)
	respHashes := make([][32]byte, len(respSegs))
	for i, s := range respSegs {
		respHashes[i] = s.Hash
	}

	p := &prepared{rec: rec, epoch: ep}

	// Classify blobs: already stored (maybe bump the retention timestamp) or new.
	segs := append(append([]capture.Segment{}, reqSegs...), respSegs...)
	seen := map[[32]byte]bool{}
	var upload []store.Blob
	r.stMu.Lock()
	for _, s := range segs {
		if seen[s.Hash] {
			continue
		}
		seen[s.Hash] = true
		p.hashes = append(p.hashes, s.Hash)
		if at, ok := r.stored[s.Hash]; ok {
			if at.Before(rec.StartedAt.Add(-bumpSkew)) {
				p.bumps = append(p.bumps, store.BlobRef{Hash: s.Hash, At: rec.StartedAt})
			}
			continue
		}
		b := store.Blob{Hash: s.Hash, Kind: s.Kind, Data: s.Data}
		p.newBlobs = append(p.newBlobs, b)
		if r.st.ExternalBlobs() {
			if _, ok := r.uploaded[s.Hash]; !ok {
				upload = append(upload, b)
			}
		}
	}
	r.stMu.Unlock()
	if len(upload) > 0 {
		if err := r.st.UploadBlobs(ctx, upload); err != nil {
			return nil, err
		}
		now := time.Now()
		r.stMu.Lock()
		for _, b := range upload {
			r.uploaded[b.Hash] = now
		}
		r.stMu.Unlock()
	}
	sid := sess.id
	p.row = store.RequestRow{
		ID:              rec.RequestID,
		StartedAt:       rec.StartedAt,
		EmployeeID:      rec.EmployeeID,
		AccountID:       rec.AccountID,
		APIKeyID:        rec.APIKeyID,
		SessionID:       &sid,
		ClientKind:      rec.ClientKind,
		UserAgent:       rec.UserAgent,
		Endpoint:        rec.Endpoint,
		Stream:          rec.Stream,
		ModelRequested:  rec.ModelRequested,
		ModelResponded:  rec.Outcome.Model,
		ReasoningEffort: rec.ReasoningEffort,
		StatusCode:      rec.StatusCode,
		ErrorType:       firstNonEmpty(rec.ErrorType, rec.Outcome.ErrorType),
		ErrorBody:       rec.ErrorBody,
		QueuedMS:        rec.QueuedMS,
		DurationMS:      rec.DurationMS,
		TokensInput:     rec.Outcome.Usage.Input,
		TokensCached:    rec.Outcome.Usage.Cached,
		TokensOutput:    rec.Outcome.Usage.Output,
		TokensReasoning: rec.Outcome.Usage.Reasoning,
		ReqPrefixLen:    enc.PrefixLen,
		ReqChainDepth:   enc.ChainDepth,
		ReqHashes:       enc.Hashes,
		ReqInputStart:   inputStart,
		RespHashes:      store.JoinHashes(respHashes),
		ClientIP:        rec.ClientIP,
		DeviceID:        rec.DeviceID,
	}
	if rec.Outcome.HasTTFT {
		ms := int(rec.Outcome.TTFT.Milliseconds())
		p.row.TTFTMs = &ms
	}
	if enc.PrefixOf != nil {
		var u [16]byte
		copy(u[:], enc.PrefixOf[:])
		p.row.ReqPrefixOf = &u
	}
	for _, t := range capture.ExtractTexts(rec.Endpoint, reqSegs, inputStart, sess.inputCount, rec.Outcome) {
		if t.Text == "" {
			continue
		}
		p.texts = append(p.texts, store.TextRow{
			RequestID: rec.RequestID, EmployeeID: rec.EmployeeID, SessionID: &sid,
			StartedAt: rec.StartedAt, Role: t.Role, Text: t.Text,
		})
	}
	p.adv = store.SessionAdvance{
		SessionID: sid, LastRequestID: rec.RequestID, FullHashes: store.JoinHashes(reqHashes),
		ChainDepth: enc.ChainDepth, InputCount: inputCount, LastAt: rec.StartedAt, Requests: 1,
	}

	// Advance the in-memory session so the next request of this session can be
	// encoded against this one before it is committed.
	id := rec.RequestID
	r.stMu.Lock()
	if r.epoch == ep {
		r.sessions[sessKey(rec)] = &sessState{id: sid, lastReq: &id, full: reqHashes,
			depth: enc.ChainDepth, inputCount: inputCount, used: time.Now()}
	}
	r.stMu.Unlock()
	return p, nil
}

func (r *Recorder) writeLoop(ctx context.Context) {
	var pending []*prepared
	var timer *time.Timer
	var timerC <-chan time.Time
	sweep := time.NewTicker(sweepGap)
	defer sweep.Stop()
	commit := func() {
		if timer != nil {
			timer.Stop()
			timer, timerC = nil, nil
		}
		if len(pending) > 0 {
			r.commit(ctx, pending)
			pending = nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			for _, p := range pending {
				r.spool(p.rec)
				r.wg.Done()
			}
			for {
				select {
				case p := <-r.out:
					r.spool(p.rec)
					r.wg.Done()
				default:
					return
				}
			}
		case p := <-r.out:
			pending = append(pending, p)
			if len(pending) == 1 {
				timer = time.NewTimer(linger)
				timerC = timer.C
			}
			if len(pending) >= maxBatch {
				commit()
			}
		case <-timerC:
			timer, timerC = nil, nil
			commit()
		case <-r.kick:
			commit()
		case <-sweep.C:
			r.sweep()
		}
	}
}

// commit writes a group of prepared records in one round trip. Records
// prepared before the last failure (older epoch) may reference uncommitted
// requests, so they are spooled and re-prepared instead.
func (r *Recorder) commit(ctx context.Context, ps []*prepared) {
	r.stMu.Lock()
	ep := r.epoch
	r.stMu.Unlock()

	var lb store.LogBatch
	var ok []*prepared
	sessIdx := map[int64]int{}
	for _, p := range ps {
		if p.epoch != ep {
			r.spool(p.rec)
			r.wg.Done()
			r.replaySoon()
			continue
		}
		ok = append(ok, p)
		for _, b := range p.newBlobs {
			lb.NewBlobs = append(lb.NewBlobs, b)
			lb.NewAt = append(lb.NewAt, p.rec.StartedAt)
		}
		lb.Bumps = append(lb.Bumps, p.bumps...)
		lb.Requests = append(lb.Requests, p.row)
		lb.Texts = append(lb.Texts, p.texts...)
		if i, seen := sessIdx[p.adv.SessionID]; seen {
			n := lb.Sessions[i].Requests
			lb.Sessions[i] = p.adv // records of a session arrive in order: keep the latest
			lb.Sessions[i].Requests = n + 1
		} else {
			sessIdx[p.adv.SessionID] = len(lb.Sessions)
			lb.Sessions = append(lb.Sessions, p.adv)
		}
	}
	if len(ok) == 0 {
		return
	}

	if err := r.st.CommitLogBatch(ctx, lb); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			// Database unreachable: keep everything on disk until it is back.
			r.log.Warn("recorder: database unavailable, spooling batch", "err", err, "records", len(ok))
			r.reset()
			for _, p := range ok {
				r.spool(p.rec)
				r.wg.Done()
			}
			return
		}
		// The database rejected something: isolate it record by record so one
		// bad record cannot keep failing everyone else's.
		r.log.Warn("recorder: batch rejected, committing records one by one", "err", err, "records", len(ok))
		r.commitEach(ctx, ok)
		return
	}
	r.markStored(ok)
	for range ok {
		r.wg.Done()
	}
}

// commitEach commits records individually after a batch was rejected.
// Records the database permanently rejects go to rejected.jsonl; records whose
// prefix points at a failed record are re-prepared from the spool.
func (r *Recorder) commitEach(ctx context.Context, ps []*prepared) {
	failed := map[[16]byte]bool{}
	respool := false
	for _, p := range ps {
		if p.row.ReqPrefixOf != nil && failed[*p.row.ReqPrefixOf] {
			failed[p.rec.RequestID] = true
			r.spool(p.rec)
			respool = true
			r.wg.Done()
			continue
		}
		lb := store.LogBatch{Bumps: p.bumps, Requests: []store.RequestRow{p.row}, Texts: p.texts,
			Sessions: []store.SessionAdvance{p.adv}}
		for _, b := range p.newBlobs {
			lb.NewBlobs = append(lb.NewBlobs, b)
			lb.NewAt = append(lb.NewAt, p.rec.StartedAt)
		}
		err := r.st.CommitLogBatch(ctx, lb)
		switch {
		case err == nil:
			r.markStored([]*prepared{p})
		case permanent(err):
			failed[p.rec.RequestID] = true
			r.log.Error("recorder: record rejected by the database, set aside", "err", err, "request_id", uuidString(p.rec.RequestID))
			r.reject(p.rec, err)
		default:
			failed[p.rec.RequestID] = true
			r.spool(p.rec)
			respool = true
		}
		r.wg.Done()
	}
	// Cached session states may point at failed records: start over from
	// what is committed.
	r.reset()
	if respool {
		r.replaySoon()
	}
}

// permanent reports a data or integrity error that retrying cannot fix.
func permanent(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (strings.HasPrefix(pgErr.Code, "22") || strings.HasPrefix(pgErr.Code, "23"))
}

// reset forgets cached session states; records prepared against them are
// spooled and re-prepared.
func (r *Recorder) reset() {
	r.stMu.Lock()
	r.epoch++
	r.sessions = map[string]*sessState{}
	r.stMu.Unlock()
}

func (r *Recorder) markStored(ps []*prepared) {
	r.stMu.Lock()
	defer r.stMu.Unlock()
	for _, p := range ps {
		for _, h := range p.hashes {
			if at, seen := r.stored[h]; !seen || p.rec.StartedAt.After(at) {
				r.stored[h] = p.rec.StartedAt
			}
		}
	}
}

// replaySoon replays the spool shortly (instead of waiting for the periodic
// replay) when records were spooled while the database is reachable.
func (r *Recorder) replaySoon() {
	if r.ctx == nil || !r.replay.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.replay.Store(false)
		select {
		case <-r.ctx.Done():
		case <-time.After(time.Second):
			r.replaySpool(r.ctx)
		}
	}()
}

// reject sets a record aside in rejected.jsonl for manual inspection.
func (r *Recorder) reject(rec Record, cause error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(r.spoolDir, "rejected.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.log.Error("recorder: cannot open rejected file", "err", err)
		return
	}
	defer f.Close()
	b, _ := json.Marshal(struct {
		Error  string `json:"error"`
		Record Record `json:"record"`
	}{cause.Error(), rec})
	_, _ = f.Write(append(b, '\n'))
}

// sweep bounds memory: idle sessions and old blob memos are forgotten (they
// are re-read or re-bumped if needed again).
func (r *Recorder) sweep() {
	now := time.Now()
	r.stMu.Lock()
	defer r.stMu.Unlock()
	for k, s := range r.sessions {
		if now.Sub(s.used) > idleSess {
			delete(r.sessions, k)
		}
	}
	for h, at := range r.stored {
		if now.Sub(at) > memoTTL {
			delete(r.stored, h)
		}
	}
	for h, at := range r.uploaded {
		if now.Sub(at) > memoTTL {
			delete(r.uploaded, h)
		}
	}
}

func (r *Recorder) spoolPath() string { return filepath.Join(r.spoolDir, "pending.jsonl") }

func (r *Recorder) spool(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.MkdirAll(r.spoolDir, 0o700)
	f, err := os.OpenFile(r.spoolPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.log.Error("recorder: cannot open spool", "err", err)
		return
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		r.log.Error("recorder: cannot marshal spool record", "err", err)
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// replaySpool re-processes spooled records. Failures are re-spooled.
func (r *Recorder) replaySpool(ctx context.Context) {
	r.mu.Lock()
	path := r.spoolPath()
	if _, err := os.Stat(path); err != nil {
		r.mu.Unlock()
		return
	}
	replaying := path + ".replaying"
	if err := os.Rename(path, replaying); err != nil {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

	f, err := os.Open(replaying)
	if err != nil {
		return
	}
	var records []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	for scanner.Scan() {
		var rec Record
		if json.Unmarshal(scanner.Bytes(), &rec) == nil {
			records = append(records, rec)
		}
	}
	f.Close()
	_ = os.Remove(replaying)

	// Feed replayed records through the workers (blocking is fine here) so a
	// session's records are still written one at a time.
	for _, rec := range records {
		r.wg.Add(1)
		select {
		case r.shardFor(rec) <- rec:
		case <-ctx.Done():
			r.wg.Done()
			r.spool(rec)
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func uuidString(u [16]byte) string { return capture.UUID(u).String() }
