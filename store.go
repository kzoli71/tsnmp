package main

import (
	"sort"
	"sync"
	"time"
)

// RawCounters is one snapshot of an interface's counters.
type RawCounters struct {
	T         time.Time // local time of the response
	Uptime    uint32    // agent sysUpTime (1/100 s) of the same response
	HasUptime bool
	Discont   uint32 // ifCounterDiscontinuityTime (0 if not supported)
	InOct     uint64
	OutOct    uint64
	InPkts    [3]uint64 // packet counter parts (ucast/mcast/bcast or ucast/nucast)
	OutPkts   [3]uint64
	Is64Bit   bool
}

// Sample is a computed rate point.
type Sample struct {
	T      time.Time
	InBps  float64
	OutBps float64
	InPps  float64
	OutPps float64
}

// Iface holds the metadata and full sample history of one interface.
type Iface struct {
	Index   int
	Name    string
	Descr   string
	Alias   string
	Admin   int
	Oper    int
	Speed   uint64 // bit/s, 0 if unknown
	Present bool   // listed by the agent at the last discovery
	Hist    History
	last    *RawCounters
}

// Description returns ifAlias, or ifDescr when the alias is empty.
func (i *Iface) Description() string {
	if i.Alias != "" {
		return i.Alias
	}
	return i.Descr
}

func (i *Iface) AdminUp() bool { return i.Admin == 1 }
func (i *Iface) OperUp() bool  { return i.Oper == 1 }

// Latest returns the most recent sample, if any.
func (i *Iface) Latest() (Sample, bool) {
	return i.Hist.Last()
}

// Store keeps all interface data in memory for the lifetime of the program.
type Store struct {
	mu     sync.RWMutex
	ifaces map[int]*Iface
}

func NewStore() *Store {
	return &Store{ifaces: make(map[int]*Iface)}
}

// IfaceInfo is discovery data for one interface.
type IfaceInfo struct {
	Index int
	Name  string
	Descr string
	Alias string
	Admin int
	Oper  int
	Speed uint64 // bit/s
}

// UpdateInterfaces applies discovery results. Interfaces that disappeared
// from the agent are hidden but their history is kept.
func (s *Store) UpdateInterfaces(infos []IfaceInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[int]bool, len(infos))
	for _, in := range infos {
		seen[in.Index] = true
		ifc, ok := s.ifaces[in.Index]
		if !ok {
			ifc = &Iface{Index: in.Index}
			s.ifaces[in.Index] = ifc
		}
		ifc.Name, ifc.Descr, ifc.Alias = in.Name, in.Descr, in.Alias
		ifc.Admin, ifc.Oper, ifc.Speed = in.Admin, in.Oper, in.Speed
		ifc.Present = true
	}
	for idx, ifc := range s.ifaces {
		if !seen[idx] {
			ifc.Present = false
			ifc.last = nil // avoid a bogus rate if it comes back
		}
	}
}

// ApplyPoll records statuses and counters. It returns true when the agent
// reported an interface that discovery has not seen yet.
func (s *Store) ApplyPoll(d PollData) (unknown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for idx, st := range d.Status {
		ifc, ok := s.ifaces[idx]
		if !ok || !ifc.Present {
			unknown = true
			continue
		}
		ifc.Admin, ifc.Oper = st.Admin, st.Oper
	}
	for idx, rc := range d.Counters {
		ifc, ok := s.ifaces[idx]
		if !ok || !ifc.Present {
			continue
		}
		if prev := ifc.last; prev != nil {
			if smp, ok := computeRate(*prev, rc, ifc.Speed); ok {
				ifc.Hist.append(smp)
			}
		}
		rc := rc
		ifc.last = &rc
	}
	return unknown
}

// Reset deletes all measurements: the histories and the last counter
// snapshots, so the next poll only records counters and the one after it
// gives the first sample. Interfaces no longer listed by the agent are
// dropped. Earlier History snapshots stay valid (they keep their blocks).
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for idx, ifc := range s.ifaces {
		if !ifc.Present {
			delete(s.ifaces, idx)
			continue
		}
		ifc.Hist, ifc.last = History{}, nil
	}
}

// Snapshot returns copies of all present interfaces (sorted by ifIndex); their
// History values are read-only snapshots.
func (s *Store) Snapshot() []Iface {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Iface
	for _, ifc := range s.ifaces {
		if ifc.Present {
			c := *ifc
			c.last = nil
			out = append(out, c)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Index < out[b].Index })
	return out
}

// Get returns a copy of one interface.
func (s *Store) Get(idx int) (Iface, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ifc, ok := s.ifaces[idx]
	if !ok {
		return Iface{}, false
	}
	c := *ifc
	c.last = nil
	return c, true
}

// counterDelta returns cur-prev. A 32-bit counter that went backwards is
// taken as a single wrap (wrapped = true); ok is false when it cannot be one.
func counterDelta(prev, cur uint64, is64 bool) (d uint64, wrapped, ok bool) {
	if cur >= prev {
		return cur - prev, false, true
	}
	if is64 || prev > 0xFFFFFFFF {
		// A 64-bit counter practically never wraps: it was reset.
		return 0, false, false
	}
	return (0x100000000 - prev) + cur, true, true
}

// maxSpeedFactor: a rate this much above the interface speed after a 32-bit
// wrap means the counter was actually reset, not wrapped.
const maxSpeedFactor = 1.2

// computeRate turns two counter snapshots into a sample. It rejects pairs
// spanning a counter discontinuity: agent restart (sysUpTime went back),
// changed ifCounterDiscontinuityTime, a 64-bit counter going back, or a
// 32-bit "wrap" implying more traffic than the interface speed (speed in
// bit/s, 0 = unknown).
func computeRate(prev, cur RawCounters, speed uint64) (Sample, bool) {
	if prev.Is64Bit != cur.Is64Bit || prev.Discont != cur.Discont {
		return Sample{}, false
	}
	// Prefer the agent's own clock: immune to network delay and retries.
	dt := cur.T.Sub(prev.T).Seconds()
	if prev.HasUptime && cur.HasUptime {
		if cur.Uptime <= prev.Uptime {
			return Sample{}, false // agent restarted (or uptime wrapped)
		}
		dt = float64(cur.Uptime-prev.Uptime) / 100
	}
	if dt <= 0 {
		return Sample{}, false
	}
	is64 := cur.Is64Bit
	dIn, w1, ok1 := counterDelta(prev.InOct, cur.InOct, is64)
	dOut, w2, ok2 := counterDelta(prev.OutOct, cur.OutOct, is64)
	dInP, ok3 := partsDelta(prev.InPkts, cur.InPkts, is64)
	dOutP, ok4 := partsDelta(prev.OutPkts, cur.OutPkts, is64)
	if !(ok1 && ok2 && ok3 && ok4) {
		return Sample{}, false
	}
	smp := Sample{
		T:      cur.T,
		InBps:  float64(dIn) * 8 / dt,
		OutBps: float64(dOut) * 8 / dt,
		InPps:  float64(dInP) / dt,
		OutPps: float64(dOutP) / dt,
	}
	limit := float64(speed) * maxSpeedFactor
	if speed > 0 && (w1 && smp.InBps > limit || w2 && smp.OutBps > limit) {
		return Sample{}, false
	}
	return smp, true
}

// partsDelta sums the deltas of independently wrapping counters.
func partsDelta(prev, cur [3]uint64, is64 bool) (uint64, bool) {
	var sum uint64
	for i := range prev {
		d, _, ok := counterDelta(prev[i], cur[i], is64)
		if !ok {
			return 0, false
		}
		sum += d
	}
	return sum, true
}
