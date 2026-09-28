package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

// memBackend is an in-memory BlobBackend for testing the external-storage path.
type memBackend struct {
	mu   sync.Mutex
	data map[string][]byte
	puts int
}

func newMemBackend() *memBackend { return &memBackend{data: map[string][]byte{}} }

func (m *memBackend) Put(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append([]byte(nil), data...)
	m.puts++
	return nil
}
func (m *memBackend) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return b, nil
}
func (m *memBackend) Delete(_ context.Context, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}
func (m *memBackend) len() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.data) }

func hash32(b byte) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = b
	}
	return h
}

func TestBlobsExternalBackend(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	be := newMemBackend()
	st.SetBlobBackend(be)
	ctx := context.Background()
	now := time.Now()

	b1 := store.Blob{Hash: hash32(1), Kind: "input", Data: []byte("hello world content")}
	b2 := store.Blob{Hash: hash32(2), Kind: "output", Data: []byte("assistant reply content")}
	if err := st.PutBlobs(ctx, []store.Blob{b1, b2}, now); err != nil {
		t.Fatal(err)
	}

	// Bytes must be in the backend, and data_zstd must be NULL in Postgres.
	if be.len() != 2 {
		t.Fatalf("backend objects = %d, want 2", be.len())
	}
	var nullCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM blobs WHERE data_zstd IS NULL`).Scan(&nullCount); err != nil {
		t.Fatal(err)
	}
	if nullCount != 2 {
		t.Fatalf("rows with NULL data_zstd = %d, want 2", nullCount)
	}

	// Round trip through the backend.
	kind, raw, err := st.GetBlob(ctx, b1.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "input" || string(raw) != "hello world content" {
		t.Fatalf("got kind=%q raw=%q", kind, string(raw))
	}

	// Dedup: re-putting the same blob does not re-upload (puts unchanged).
	putsBefore := be.puts
	if err := st.PutBlobs(ctx, []store.Blob{b1}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if be.puts != putsBefore {
		t.Fatalf("re-put uploaded again: puts %d -> %d (dedup broken)", putsBefore, be.puts)
	}

	// Purge removes objects from the backend and rows from Postgres.
	n, err := st.PurgeExpiredBlobs(ctx, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("purge deleted 0 rows")
	}
	if be.len() != 0 {
		t.Fatalf("backend still has %d objects after purge", be.len())
	}
	if _, _, err := st.GetBlob(ctx, b1.Hash); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after purge, got %v", err)
	}
}

// failBackend rejects every upload.
type failBackend struct{ memBackend }

func (f *failBackend) Put(context.Context, string, []byte) error { return errUpload }

var errUpload = errors.New("upload failed")

func TestBlobsExternalUploadFailureWritesNoRows(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	st.SetBlobBackend(&failBackend{memBackend: *newMemBackend()})
	ctx := context.Background()

	err := st.PutBlobs(ctx, []store.Blob{{Hash: hash32(9), Kind: "input", Data: []byte("x")}}, time.Now())
	if !errors.Is(err, errUpload) {
		t.Fatalf("err = %v, want upload error", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM blobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rows = %d, want 0 (no row without stored bytes)", n)
	}
}

func TestBlobsExternalDuplicateHashesInOneCall(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	be := newMemBackend()
	st.SetBlobBackend(be)
	b := store.Blob{Hash: hash32(7), Kind: "tools", Data: []byte("same tools segment")}
	if err := st.PutBlobs(context.Background(), []store.Blob{b, b, b}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if be.puts != 1 {
		t.Fatalf("uploads = %d, want 1", be.puts)
	}
}
