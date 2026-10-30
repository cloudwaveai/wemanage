package workrecords

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"aiseat/internal/store"
)

type fakeReader struct {
	enc   map[[16]byte]store.RequestEnc
	blobs map[[32]byte][]byte
}

func (f fakeReader) GetRequestEnc(_ context.Context, id [16]byte) (store.RequestEnc, error) {
	v, ok := f.enc[id]
	if !ok {
		return v, errors.New("missing request")
	}
	return v, nil
}
func (f fakeReader) GetBlob(_ context.Context, h [32]byte) (string, []byte, error) {
	v, ok := f.blobs[h]
	if !ok {
		return "", nil, errors.New("missing blob")
	}
	return "input", v, nil
}
func TestReconstructPreservesFullOrderAndRejectsGaps(t *testing.T) {
	a, b, c := []byte(`{"content":"第一条"}`), []byte(`{"content":"第二条"}`), []byte(`{"content":"回复"}`)
	ha, hb, hc := sha256.Sum256(a), sha256.Sum256(b), sha256.Sum256(c)
	parent, child := [16]byte{1}, [16]byte{2}
	f := fakeReader{enc: map[[16]byte]store.RequestEnc{parent: {Hashes: ha[:]}, child: {PrefixOf: &parent, PrefixLen: 1, Hashes: hb[:]}}, blobs: map[[32]byte][]byte{ha: a, hb: b, hc: c}}
	row := store.RequestRow{ID: child, RespHashes: hc[:]}
	r, err := Reconstruct(context.Background(), f, row)
	if err != nil || len(r.Request) != 2 || string(r.Request[0].Data) != string(a) || string(r.Request[1].Data) != string(b) || len(r.Response) != 1 || string(r.Response[0].Data) != string(c) {
		t.Fatalf("reconstruction: %#v %v", r, err)
	}
	delete(f.blobs, hb)
	if _, err = Reconstruct(context.Background(), f, row); err == nil {
		t.Fatal("missing piece accepted")
	}
	f.blobs[hb] = []byte("altered")
	if _, err = Reconstruct(context.Background(), f, row); err == nil {
		t.Fatal("corrupt piece accepted")
	}
	row.BodiesPurged = true
	if _, err = Reconstruct(context.Background(), f, row); err == nil {
		t.Fatal("purged record accepted")
	}
}
func TestRejectsInvalidPrefixCycleAndResponseIndex(t *testing.T) {
	id := [16]byte{1}
	f := fakeReader{enc: map[[16]byte]store.RequestEnc{id: {PrefixOf: &id}}, blobs: map[[32]byte][]byte{}}
	if _, err := Reconstruct(context.Background(), f, store.RequestRow{ID: id}); err == nil {
		t.Fatal("cycle accepted")
	}
	f.enc[id] = store.RequestEnc{}
	if _, err := Reconstruct(context.Background(), f, store.RequestRow{ID: id, RespHashes: []byte{1}}); err == nil {
		t.Fatal("invalid response index accepted")
	}
}
