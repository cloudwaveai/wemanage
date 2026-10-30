// Package workrecords reconstructs stored, redacted gateway records without
// silently omitting unavailable pieces. It makes no model calls.
package workrecords

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"aiseat/internal/store"
)

type Reader interface {
	GetRequestEnc(context.Context, [16]byte) (store.RequestEnc, error)
	GetBlob(context.Context, [32]byte) (string, []byte, error)
}

type Segment struct {
	Hash [32]byte
	Kind string
	Data []byte
}
type Record struct {
	Request  []Segment
	Response []Segment
}

func hashes(raw []byte) ([][32]byte, error) {
	if len(raw)%32 != 0 {
		return nil, errors.New("invalid segment index length")
	}
	out := make([][32]byte, len(raw)/32)
	for i := range out {
		copy(out[i][:], raw[i*32:i*32+32])
	}
	return out, nil
}

// requestHashes walks an untrusted stored prefix chain with explicit cycle and
// length checks. It returns every ordered segment, including repeated hashes.
func requestHashes(ctx context.Context, db Reader, id [16]byte) ([][32]byte, error) {
	seen := map[[16]byte]bool{}
	var chain []store.RequestEnc
	for {
		if seen[id] || len(chain) > 32 {
			return nil, errors.New("invalid prefix chain")
		}
		seen[id] = true
		enc, err := db.GetRequestEnc(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read prefix: %w", err)
		}
		chain = append(chain, enc)
		if enc.PrefixOf == nil {
			break
		}
		id = *enc.PrefixOf
	}
	var full [][32]byte
	for i := len(chain) - 1; i >= 0; i-- {
		enc := chain[i]
		tail, err := hashes(enc.Hashes)
		if err != nil {
			return nil, err
		}
		if enc.PrefixOf != nil {
			if enc.PrefixLen < 0 || enc.PrefixLen > len(full) {
				return nil, errors.New("invalid prefix length")
			}
			full = full[:enc.PrefixLen]
		} else if enc.PrefixLen != 0 {
			return nil, errors.New("invalid root prefix")
		}
		full = append(full, tail...)
	}
	return full, nil
}

// Reconstruct returns an error for purged, missing, corrupt or empty records.
// Callers must record the affected request as a coverage gap, not replace it
// with truncated search-index text or present the day's report as complete.
func Reconstruct(ctx context.Context, db Reader, row store.RequestRow) (Record, error) {
	if row.BodiesPurged {
		return Record{}, errors.New("record body purged")
	}
	req, err := requestHashes(ctx, db, row.ID)
	if err != nil {
		return Record{}, err
	}
	resp, err := hashes(row.RespHashes)
	if err != nil {
		return Record{}, err
	}
	if len(req)+len(resp) == 0 {
		return Record{}, errors.New("record has no stored segments")
	}
	load := func(hs [][32]byte) ([]Segment, error) {
		out := make([]Segment, 0, len(hs))
		for _, h := range hs {
			kind, data, err := db.GetBlob(ctx, h)
			if err != nil {
				return nil, fmt.Errorf("segment unavailable: %w", err)
			}
			if sha256.Sum256(data) != h {
				return nil, errors.New("segment hash mismatch")
			}
			out = append(out, Segment{Hash: h, Kind: kind, Data: data})
		}
		return out, nil
	}
	a, err := load(req)
	if err != nil {
		return Record{}, err
	}
	b, err := load(resp)
	if err != nil {
		return Record{}, err
	}
	return Record{Request: a, Response: b}, nil
}
