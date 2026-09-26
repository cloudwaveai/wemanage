package web

import (
	"context"
	"encoding/json"

	"aiseat/internal/capture"
	"aiseat/internal/store"
)

// transcriptSegment is one reconstructed piece of a request/response.
type transcriptSegment struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Transcript is a reconstructed request+response view.
type Transcript struct {
	Request  []transcriptSegment `json:"request"`
	Response []transcriptSegment `json:"response"`
	Purged   bool                `json:"purged"`
}

// reconstruct rebuilds a request's full request and response bodies from blobs.
func (s *Server) reconstruct(ctx context.Context, row store.RequestRow) (Transcript, error) {
	var t Transcript
	t.Purged = row.BodiesPurged
	if row.BodiesPurged {
		return t, nil
	}

	// Request hashes via the prefix chain.
	enc, err := s.deps.Store.GetRequestEnc(ctx, row.ID)
	if err != nil {
		return t, err
	}
	reqHashes, err := capture.Decode(toCaptureEnc(enc), func(id capture.UUID) (capture.Encoded, error) {
		e, err := s.deps.Store.GetRequestEnc(ctx, [16]byte(id))
		if err != nil {
			return capture.Encoded{}, err
		}
		return toCaptureEnc(e), nil
	})
	if err != nil {
		return t, err
	}
	for _, h := range reqHashes {
		kind, raw, err := s.deps.Store.GetBlob(ctx, h)
		if err != nil {
			continue // purged or missing; skip
		}
		t.Request = append(t.Request, transcriptSegment{Kind: kind, Data: json.RawMessage(raw)})
	}
	for _, h := range splitHashes(row.RespHashes) {
		kind, raw, err := s.deps.Store.GetBlob(ctx, h)
		if err != nil {
			continue
		}
		t.Response = append(t.Response, transcriptSegment{Kind: kind, Data: json.RawMessage(raw)})
	}
	return t, nil
}

func toCaptureEnc(e store.RequestEnc) capture.Encoded {
	out := capture.Encoded{PrefixLen: e.PrefixLen, ChainDepth: e.ChainDepth, Hashes: e.Hashes}
	if e.PrefixOf != nil {
		u := capture.UUID(*e.PrefixOf)
		out.PrefixOf = &u
	}
	return out
}

func splitHashes(b []byte) [][32]byte {
	n := len(b) / 32
	out := make([][32]byte, 0, n)
	for i := 0; i < n; i++ {
		var h [32]byte
		copy(h[:], b[i*32:(i+1)*32])
		out = append(out, h)
	}
	return out
}
