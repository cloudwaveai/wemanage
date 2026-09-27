package risk

import (
	"sort"
	"sync"
	"time"
)

// sighting is one (device, coarse network) pair seen recently for an employee.
type sighting struct {
	Device    int64
	Net       string // coarse network key (/16 or /32)
	At        time.Time
	Trusted   bool
	FirstSeen time.Time // device first seen
}

// window keeps each employee's recent sightings in memory. It is the basis of
// the concurrency rules: the same key in use from different networks at the
// same time is the clearest sign that it has been shared.
type window struct {
	mu    sync.Mutex
	byEmp map[int64][]sighting
}

func newWindow() *window { return &window{byEmp: make(map[int64][]sighting)} }

// add records s, drops sightings older than span and returns a snapshot.
func (w *window) add(emp int64, s sighting, span time.Duration) []sighting {
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := s.At.Add(-span)
	cur := w.byEmp[emp]
	out := cur[:0]
	merged := false
	for _, x := range cur {
		if x.At.Before(cutoff) {
			continue
		}
		if x.Device == s.Device {
			x.Trusted = s.Trusted // status is per device, keep it current
			if x.Net == s.Net {
				x.At = s.At
				merged = true
			}
		}
		out = append(out, x)
	}
	if !merged {
		out = append(out, s)
	}
	w.byEmp[emp] = out
	return append([]sighting(nil), out...)
}

// setTrusted updates the trust flag of a device's sightings.
func (w *window) setTrusted(emp, device int64, trusted bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.byEmp[emp] {
		if w.byEmp[emp][i].Device == device {
			w.byEmp[emp][i].Trusted = trusted
		}
	}
}

// forget drops an employee's sightings (after an admin unfreeze).
func (w *window) forget(emp int64) {
	w.mu.Lock()
	delete(w.byEmp, emp)
	w.mu.Unlock()
}

// finding is a high-severity rule hit from the concurrency window.
type finding struct {
	Rule     string
	DeviceID int64
	Detail   map[string]any
}

// evalWindow applies the concurrency rules to a window snapshot:
//
//   - multi_network: minNets or more distinct coarse networks in the window,
//     whatever the devices' trust status;
//   - concurrent_untrusted: an untrusted device older than grace is active at
//     the same time as another device on a different network.
func evalWindow(ss []sighting, now time.Time, grace time.Duration, minNets int) []finding {
	var out []finding
	nets := map[string]bool{}
	for _, s := range ss {
		nets[s.Net] = true
	}
	if len(nets) >= minNets {
		out = append(out, finding{Rule: RuleMultiNetwork, Detail: map[string]any{"networks": sortedKeys(nets)}})
	}
	for _, a := range ss {
		if a.Trusted || now.Sub(a.FirstSeen) < grace {
			continue
		}
		for _, b := range ss {
			if b.Device != a.Device && b.Net != a.Net {
				out = append(out, finding{Rule: RuleConcurrentUntrusted, DeviceID: a.Device, Detail: map[string]any{
					"device_id": a.Device, "network": a.Net,
					"other_device_id": b.Device, "other_network": b.Net,
				}})
				return out
			}
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
