package capture

import (
	"bytes"
	"fmt"
	"testing"
)

// hh builds a distinct 32-byte hash filled with n.
func hh(n byte) [32]byte {
	var x [32]byte
	for i := range x {
		x[i] = n
	}
	return x
}

// uu builds a distinct UUID whose first byte is n.
func uu(n byte) UUID {
	var x UUID
	x[0] = n
	return x
}

func eqHashes(a, b [][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEncodePrefixAndFullStore(t *testing.T) {
	a, b, c := hh(1), hh(2), hh(3)
	prev := &PrevState{ID: uu(9), Full: [][32]byte{a, b}, ChainDepth: 0}

	// hashes extends prev by one -> prefix compression.
	enc := Encode([][32]byte{a, b, c}, prev)
	if enc.PrefixOf == nil {
		t.Fatalf("expected PrefixOf set")
	}
	if *enc.PrefixOf != prev.ID {
		t.Fatalf("PrefixOf = %v, want %v", *enc.PrefixOf, prev.ID)
	}
	if enc.PrefixLen != 2 {
		t.Fatalf("PrefixLen = %d, want 2", enc.PrefixLen)
	}
	if enc.ChainDepth != 1 {
		t.Fatalf("ChainDepth = %d, want 1", enc.ChainDepth)
	}
	if !bytes.Equal(enc.Hashes, flattenHashes([][32]byte{c})) {
		t.Fatalf("Hashes tail = %x, want %x", enc.Hashes, flattenHashes([][32]byte{c}))
	}

	// Non-prefix hashes with the same prev -> full store.
	x := hh(42)
	full := Encode([][32]byte{x}, prev)
	if full.PrefixOf != nil {
		t.Fatalf("expected full store (PrefixOf nil), got %v", *full.PrefixOf)
	}
	if full.PrefixLen != 0 || full.ChainDepth != 0 {
		t.Fatalf("full store fields = {len %d depth %d}, want {0,0}", full.PrefixLen, full.ChainDepth)
	}
	if !bytes.Equal(full.Hashes, flattenHashes([][32]byte{x})) {
		t.Fatalf("full store Hashes = %x, want %x", full.Hashes, flattenHashes([][32]byte{x}))
	}
}

func TestEncodeDecodeChain(t *testing.T) {
	a, b, c := hh(1), hh(2), hh(3)
	id1, id2, id3 := uu(1), uu(2), uu(3)

	store := map[UUID]Encoded{}
	fetch := func(id UUID) (Encoded, error) {
		e, ok := store[id]
		if !ok {
			return Encoded{}, fmt.Errorf("no such request %s", id)
		}
		return e, nil
	}

	enc1 := Encode([][32]byte{a}, nil)
	store[id1] = enc1
	if enc1.PrefixOf != nil {
		t.Fatalf("root should have nil PrefixOf")
	}

	enc2 := Encode([][32]byte{a, b}, &PrevState{ID: id1, Full: [][32]byte{a}, ChainDepth: enc1.ChainDepth})
	store[id2] = enc2
	if enc2.PrefixOf == nil || *enc2.PrefixOf != id1 || enc2.PrefixLen != 1 || enc2.ChainDepth != 1 {
		t.Fatalf("enc2 unexpected: %+v", enc2)
	}

	enc3 := Encode([][32]byte{a, b, c}, &PrevState{ID: id2, Full: [][32]byte{a, b}, ChainDepth: enc2.ChainDepth})
	store[id3] = enc3
	if enc3.PrefixOf == nil || *enc3.PrefixOf != id2 || enc3.PrefixLen != 2 || enc3.ChainDepth != 2 {
		t.Fatalf("enc3 unexpected: %+v", enc3)
	}

	got, err := Decode(enc3, fetch)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if want := [][32]byte{a, b, c}; !eqHashes(got, want) {
		t.Fatalf("Decode = %v, want %v", got, want)
	}

	// Decoding a root returns its own list.
	gotRoot, err := Decode(enc1, fetch)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	if !eqHashes(gotRoot, [][32]byte{a}) {
		t.Fatalf("Decode root = %v, want [a]", gotRoot)
	}
}

func TestEncodeForcedFullStoreAtMaxDepth(t *testing.T) {
	a, b, c := hh(1), hh(2), hh(3)
	// prev is a valid strict prefix, but its chain is already at the limit.
	prev := &PrevState{ID: uu(7), Full: [][32]byte{a, b}, ChainDepth: MaxChainDepth}

	enc := Encode([][32]byte{a, b, c}, prev)
	if enc.PrefixOf != nil {
		t.Fatalf("at MaxChainDepth expected forced full store, got PrefixOf %v", *enc.PrefixOf)
	}
	if enc.ChainDepth != 0 {
		t.Fatalf("forced full store ChainDepth = %d, want 0", enc.ChainDepth)
	}
	if !bytes.Equal(enc.Hashes, flattenHashes([][32]byte{a, b, c})) {
		t.Fatalf("forced full store must hold the entire list")
	}
}

func TestDecodeRejectsMisalignedBlob(t *testing.T) {
	_, err := Decode(Encoded{Hashes: []byte{1, 2, 3}}, nil)
	if err == nil {
		t.Fatalf("expected error for non-multiple-of-32 blob")
	}
}
