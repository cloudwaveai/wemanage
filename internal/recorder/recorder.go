// Package recorder asynchronously persists request/response logs: it splits
// bodies into deduplicated, redacted segments, prefix-compresses each session's
// hash list, extracts searchable text, and spools to disk when the DB is down.
package recorder

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/settings"
	"aiseat/internal/store"
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
}

// Recorder buffers Records and persists them on a background worker.
type Recorder struct {
	st       *store.Store
	set      *settings.Service
	spoolDir string
	log      *slog.Logger

	ch      chan Record
	wg      sync.WaitGroup // tracks in-flight records for Flush (NOT the worker goroutine)
	mu      sync.Mutex     // guards spool file writes
	started bool
}

// New creates a Recorder with the given queue capacity.
func New(st *store.Store, set *settings.Service, spoolDir string, log *slog.Logger, queue int) *Recorder {
	if queue <= 0 {
		queue = 1024
	}
	if log == nil {
		log = slog.Default()
	}
	return &Recorder{st: st, set: set, spoolDir: spoolDir, log: log, ch: make(chan Record, queue)}
}

// Start replays any spooled records and launches the worker loop.
func (r *Recorder) Start(ctx context.Context) {
	r.started = true
	_ = os.MkdirAll(r.spoolDir, 0o700)
	r.replaySpool(ctx)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case rec := <-r.ch:
				r.handle(ctx, rec)
				r.wg.Done()
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
	case r.ch <- rec:
	default:
		r.wg.Done()
		r.spool(rec)
	}
}

// Flush blocks until all submitted records are processed (test helper).
func (r *Recorder) Flush() { r.wg.Wait() }

func (r *Recorder) handle(ctx context.Context, rec Record) {
	if err := r.process(ctx, rec); err != nil {
		r.log.Warn("recorder: persist failed, spooling", "err", err, "request_id", uuidString(rec.RequestID))
		r.spool(rec)
	}
}

// process performs the full persistence pipeline for one record.
func (r *Recorder) process(ctx context.Context, rec Record) error {
	set := r.set.Get(ctx)
	red, err := capture.NewRedactor(set.RedactionRules)
	if err != nil {
		return err
	}

	reqSegs, inputStart, err := capture.SplitRequest(rec.Endpoint, rec.ReqBody, red)
	if err != nil {
		return err
	}
	respSegs := capture.SplitResponse(rec.Outcome, red)

	// Deduplicated blobs for every segment (dedup happens on hash).
	blobs := make([]store.Blob, 0, len(reqSegs)+len(respSegs))
	for _, s := range reqSegs {
		blobs = append(blobs, store.Blob{Hash: s.Hash, Kind: s.Kind, Data: s.Data})
	}
	for _, s := range respSegs {
		blobs = append(blobs, store.Blob{Hash: s.Hash, Kind: s.Kind, Data: s.Data})
	}
	if err := r.st.PutBlobs(ctx, blobs, rec.StartedAt); err != nil {
		return err
	}

	prev, err := r.st.BeginSession(ctx, rec.EmployeeID, rec.AccountID, rec.SessionKey, rec.ClientKind, rec.StartedAt)
	if err != nil {
		return err
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
	if prev.PrevRequestID != nil {
		prevState = &capture.PrevState{
			ID:         capture.UUID(*prev.PrevRequestID),
			Full:       prev.PrevFull,
			ChainDepth: prev.PrevChainDepth,
		}
	}
	enc := capture.Encode(reqHashes, prevState)

	respHashes := make([][32]byte, len(respSegs))
	for i, s := range respSegs {
		respHashes[i] = s.Hash
	}

	row := store.RequestRow{
		ID:              rec.RequestID,
		StartedAt:       rec.StartedAt,
		EmployeeID:      rec.EmployeeID,
		AccountID:       rec.AccountID,
		APIKeyID:        rec.APIKeyID,
		SessionID:       &prev.ID,
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
	}
	if rec.Outcome.HasTTFT {
		ms := int(rec.Outcome.TTFT.Milliseconds())
		row.TTFTMs = &ms
	}
	if enc.PrefixOf != nil {
		var u [16]byte
		copy(u[:], enc.PrefixOf[:])
		row.ReqPrefixOf = &u
	}
	if err := r.st.InsertRequest(ctx, row); err != nil {
		return err
	}

	texts := capture.ExtractTexts(rec.Endpoint, reqSegs, inputStart, prev.PrevInputCount, rec.Outcome)
	trows := make([]store.TextRow, 0, len(texts))
	for _, t := range texts {
		if t.Text == "" {
			continue
		}
		trows = append(trows, store.TextRow{
			RequestID:  rec.RequestID,
			EmployeeID: rec.EmployeeID,
			SessionID:  &prev.ID,
			StartedAt:  rec.StartedAt,
			Role:       t.Role,
			Text:       t.Text,
		})
	}
	if err := r.st.InsertTexts(ctx, trows); err != nil {
		return err
	}

	return r.st.UpdateSessionAfterRequest(ctx, prev.ID, rec.RequestID,
		store.JoinHashes(reqHashes), enc.ChainDepth, inputCount, rec.StartedAt)
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

	for _, rec := range records {
		if err := r.process(ctx, rec); err != nil {
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
