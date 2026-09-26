package capture

import (
	"encoding/hex"
	"fmt"
)

// UUID identifies a request within a session.
//
// NOTE ON github.com/google/uuid: the module task listed google/uuid as an
// available dependency, but that module is not declared in go.mod's require
// block — it is only present transitively (recorded in go.sum via
// pressly/goose). Importing it directly fails `go build` under the default
// -mod=readonly ("updates to go.mod needed; to update it: go mod tidy"), and
// modifying go.mod / running `go mod tidy` is explicitly disallowed. To keep
// the package building with the mandated command, UUID is defined locally with
// the same 16-byte shape and canonical 8-4-4-4-12 String() form as
// google/uuid.UUID, so it is a drop-in replacement for the fields and
// signatures described by the task.
type UUID [16]byte

// String renders the UUID in canonical lowercase hyphenated form.
func (u UUID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], u[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], u[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], u[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], u[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], u[10:16])
	return string(buf[:])
}

// MaxChainDepth caps how many prefix-compression links may chain before a
// request is forced to store its full hash list.
const MaxChainDepth = 32

// Encoded is the stored, prefix-compressed representation of a request's
// ordered segment-hash list.
type Encoded struct {
	PrefixOf   *UUID  // request this one extends, or nil for a full (root) store
	PrefixLen  int    // number of leading hashes inherited from PrefixOf's full list
	ChainDepth int    // number of prefix links from this store back to a root
	Hashes     []byte // concatenated 32-byte hashes: the NEW tail when PrefixOf != nil, else the full list
}

// PrevState carries the previous request's fully reconstructed hash list, which
// Encode needs in order to test the prefix relationship.
type PrevState struct {
	ID         UUID
	Full       [][32]byte
	ChainDepth int
}

// Encode performs prefix compression. hashes is the current request's full,
// ordered hash list. prev (may be nil) is the previous request in the same
// session. When prev is non-nil, its chain depth is below the limit, and prev's
// full list is a strict prefix of hashes, the result stores only the new tail
// and points back at prev. Otherwise the result stores the full list as a root.
func Encode(hashes [][32]byte, prev *PrevState) Encoded {
	if prev != nil && prev.ChainDepth < MaxChainDepth && hasStrictPrefix(hashes, prev.Full) {
		id := prev.ID
		tail := hashes[len(prev.Full):]
		return Encoded{
			PrefixOf:   &id,
			PrefixLen:  len(prev.Full),
			ChainDepth: prev.ChainDepth + 1,
			Hashes:     flattenHashes(tail),
		}
	}
	return Encoded{
		PrefixOf:   nil,
		PrefixLen:  0,
		ChainDepth: 0,
		Hashes:     flattenHashes(hashes),
	}
}

// Decode reconstructs the full ordered hash list from a stored Encoded value.
// fetch resolves a request id to its own Encoded value (whose PrefixOf, in
// turn, names the next id to fetch), letting Decode walk the prefix chain back
// to a root.
func Decode(enc Encoded, fetch func(id UUID) (Encoded, error)) ([][32]byte, error) {
	tail, err := unflattenHashes(enc.Hashes)
	if err != nil {
		return nil, err
	}
	if enc.PrefixOf == nil {
		return tail, nil
	}
	prevEnc, err := fetch(*enc.PrefixOf)
	if err != nil {
		return nil, fmt.Errorf("capture: fetch prefix %s: %w", enc.PrefixOf, err)
	}
	prevFull, err := Decode(prevEnc, fetch)
	if err != nil {
		return nil, err
	}
	if enc.PrefixLen < 0 || enc.PrefixLen > len(prevFull) {
		return nil, fmt.Errorf("capture: prefix length %d out of range for previous list of %d", enc.PrefixLen, len(prevFull))
	}
	full := make([][32]byte, 0, enc.PrefixLen+len(tail))
	full = append(full, prevFull[:enc.PrefixLen]...)
	full = append(full, tail...)
	return full, nil
}

// hasStrictPrefix reports whether prefix is a proper (strictly shorter) prefix
// of hashes: every element matches and hashes carries at least one extra hash.
func hasStrictPrefix(hashes, prefix [][32]byte) bool {
	if len(prefix) >= len(hashes) {
		return false
	}
	for i := range prefix {
		if hashes[i] != prefix[i] {
			return false
		}
	}
	return true
}

func flattenHashes(hs [][32]byte) []byte {
	out := make([]byte, 0, len(hs)*32)
	for i := range hs {
		out = append(out, hs[i][:]...)
	}
	return out
}

func unflattenHashes(b []byte) ([][32]byte, error) {
	if len(b)%32 != 0 {
		return nil, fmt.Errorf("capture: hash blob length %d is not a multiple of 32", len(b))
	}
	n := len(b) / 32
	out := make([][32]byte, n)
	for i := 0; i < n; i++ {
		copy(out[i][:], b[i*32:(i+1)*32])
	}
	return out, nil
}
