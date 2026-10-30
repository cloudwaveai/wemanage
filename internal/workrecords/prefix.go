package workrecords

// PrefixReuse records a verifiable relationship between two ordered request
// lists in the same employee's session. The caller persists the preceding
// request ID and the complete current ordered hash list for reconstruction.
// Identical content in separate sessions is never automatically deduplicated.
type PrefixReuse struct {
	PreviousRequest [16]byte
	SharedSegments  int
	Remaining       []Segment
}

func ReuseSessionPrefix(previousID [16]byte, previous, current []Segment) PrefixReuse {
	shared := 0
	for shared < len(previous) && shared < len(current) && previous[shared].Hash == current[shared].Hash && previous[shared].Kind == current[shared].Kind {
		shared++
	}
	return PrefixReuse{PreviousRequest: previousID, SharedSegments: shared, Remaining: current[shared:]}
}
