package workrecords

import "testing"

func TestReuseOnlyIdenticalOrderedPrefix(t *testing.T) {
	a := Segment{Hash: [32]byte{1}, Kind: "input"}
	b := Segment{Hash: [32]byte{2}, Kind: "input"}
	c := Segment{Hash: [32]byte{3}, Kind: "input"}
	previous := []Segment{a, b}
	current := []Segment{a, b, c, b}
	r := ReuseSessionPrefix([16]byte{9}, previous, current)
	if r.SharedSegments != 2 || len(r.Remaining) != 2 || r.Remaining[1].Hash != b.Hash || r.PreviousRequest[0] != 9 {
		t.Fatal("incorrect prefix or loss of repeated independent occurrence")
	}
	r = ReuseSessionPrefix([16]byte{9}, previous, []Segment{b, a})
	if r.SharedSegments != 0 || len(r.Remaining) != 2 {
		t.Fatal("non-prefix content was removed")
	}
}
