package workrecords

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestSplitReassemblesWithoutLosingCharacters(t *testing.T) {
	text := strings.Repeat("中文😀ab", 1500)
	seg := Segment{Hash: sha256.Sum256([]byte(text)), Kind: "input", Data: []byte(text)}
	parts, err := Split(seg, 113)
	if err != nil || len(parts) < 2 {
		t.Fatalf("split: %v", err)
	}
	var rebuilt strings.Builder
	for _, p := range parts {
		if p.Start != rebuilt.Len() || p.End-p.Start != len(p.Text) || p.Total != len(text) || p.Hash != seg.Hash || len(p.Text) > 113 {
			t.Fatalf("incorrect offsets: %+v", p)
		}
		rebuilt.WriteString(p.Text)
	}
	if rebuilt.String() != text {
		t.Fatal("split lost or changed source")
	}
}
func TestSplitRejectsInvalidInput(t *testing.T) {
	if _, err := Split(Segment{Data: []byte{0xff}}, 100); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := Split(Segment{Data: []byte("ok")}, 3); err == nil {
		t.Fatal("invalid limit accepted")
	}
}
