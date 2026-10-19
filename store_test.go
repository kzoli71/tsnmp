package main

import (
	"testing"
	"time"
	"unsafe"
)

func TestCounterDelta(t *testing.T) {
	cases := []struct {
		prev, cur   uint64
		is64        bool
		want        uint64
		wrapped, ok bool
	}{
		{100, 200, false, 100, false, true},
		{0xFFFFFF00, 0x10, false, 0x110, true, true}, // 32-bit wrap
		{1 << 40, 5, true, 0, false, false},          // 64-bit reset
		{1 << 40, 5, false, 0, false, false},         // impossible for 32-bit
	}
	for _, c := range cases {
		got, w, ok := counterDelta(c.prev, c.cur, c.is64)
		if got != c.want || w != c.wrapped || ok != c.ok {
			t.Errorf("counterDelta(%d,%d,%v)=%d,%v,%v want %d,%v,%v",
				c.prev, c.cur, c.is64, got, w, ok, c.want, c.wrapped, c.ok)
		}
	}
}

func TestComputeRateDiscontinuities(t *testing.T) {
	t0 := time.Unix(1000, 0)
	base := RawCounters{T: t0, Uptime: 10000, HasUptime: true, InOct: 1000, Is64Bit: true}
	next := func(f func(*RawCounters)) RawCounters {
		c := base
		c.T = t0.Add(7 * time.Second) // local time is off (network delay) ...
		c.Uptime = 10500              // ... the agent says 5 s passed
		c.InOct = 1000 + 5*1000
		f(&c)
		return c
	}
	if smp, ok := computeRate(base, next(func(*RawCounters) {}), 0); !ok || smp.InBps != 8000 {
		t.Errorf("uptime based rate = %+v, %v; want 8000 b/s", smp, ok)
	}
	if _, ok := computeRate(base, next(func(c *RawCounters) { c.Uptime = 50 }), 0); ok {
		t.Error("agent reboot (uptime went back) not rejected")
	}
	if _, ok := computeRate(base, next(func(c *RawCounters) { c.Discont = 777 }), 0); ok {
		t.Error("changed ifCounterDiscontinuityTime not rejected")
	}

	// 32-bit: a real wrap is accepted, a counter reset that looks like a
	// wrap is rejected by the interface speed.
	b32 := RawCounters{T: t0, Uptime: 10000, HasUptime: true, InOct: 0xFFFF0000}
	wrap := b32
	wrap.Uptime, wrap.InOct = 10500, 0x10000 // 128 KB in 5 s
	if _, ok := computeRate(b32, wrap, 100_000_000); !ok {
		t.Error("plausible 32-bit wrap rejected")
	}
	// "clear counters" at 1 MB: read as a wrap that is ~4 GB in 5 s
	before := RawCounters{T: t0, Uptime: 10000, HasUptime: true, InOct: 1_000_000}
	reset := before
	reset.Uptime, reset.InOct = 10500, 5000
	if _, ok := computeRate(before, reset, 100_000_000); ok {
		t.Error("32-bit counter reset not rejected on a 100 Mb/s port")
	}
}

func TestHistoryPacked(t *testing.T) {
	if sz := unsafe.Sizeof(packedSample{}); sz != 20 {
		t.Errorf("packedSample is %d bytes", sz)
	}
	var h History
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	for i := 0; i < 3*blockLen+7; i++ {
		h.append(Sample{T: t0.Add(time.Duration(i) * time.Second), InBps: 1e9 + float64(i), OutPps: 0.5})
	}
	snap := h // a copy stays valid while the original grows
	h.append(Sample{T: t0.Add(time.Hour)})
	if snap.Len() != 3*blockLen+7 || h.Len() != snap.Len()+1 {
		t.Fatalf("len = %d / %d", snap.Len(), h.Len())
	}
	s := snap.At(blockLen + 3)
	if !s.T.Equal(t0.Add(time.Duration(blockLen+3)*time.Second)) || s.OutPps != 0.5 {
		t.Errorf("sample = %+v", s)
	}
	if rel := (s.InBps - (1e9 + float64(blockLen+3))) / 1e9; rel > 1e-6 || rel < -1e-6 {
		t.Errorf("float32 precision: %v", s.InBps)
	}
	if r := snap.Range(5, 9); len(r) != 4 || !r[0].T.Equal(t0.Add(5*time.Second)) {
		t.Errorf("Range = %+v", r)
	}
}

func TestStoreRates(t *testing.T) {
	s := NewStore()
	s.UpdateInterfaces([]IfaceInfo{{Index: 1, Name: "eth0", Admin: 1, Oper: 1}, {Index: 2, Admin: 2, Oper: 2}})
	poll := func(at time.Duration, c1, c2 RawCounters) bool {
		t0 := time.Unix(1000, 0)
		c1.T, c2.T = t0.Add(at), t0.Add(at)
		c1.Is64Bit, c2.Is64Bit = true, true
		return s.ApplyPoll(PollData{
			Status:   map[int]IfStatus{1: {1, 1}, 2: {2, 2}},
			Counters: map[int]RawCounters{1: c1, 2: c2},
		})
	}
	poll(0, RawCounters{InOct: 1000, InPkts: [3]uint64{10}}, RawCounters{})
	poll(5*time.Second, RawCounters{InOct: 6000, OutOct: 500, InPkts: [3]uint64{20, 5, 5}, OutPkts: [3]uint64{10}},
		RawCounters{InOct: 50})

	ifc, _ := s.Get(1)
	if ifc.Hist.Len() != 1 {
		t.Fatalf("samples = %d", ifc.Hist.Len())
	}
	smp := ifc.Hist.At(0)
	if smp.InBps != 8000 || smp.OutBps != 800 || smp.InPps != 4 || smp.OutPps != 2 {
		t.Errorf("unexpected sample %+v", smp)
	}
	// Admin down interfaces are measured too.
	if down, _ := s.Get(2); down.Hist.Len() != 1 || down.Hist.At(0).InBps != 80 {
		t.Errorf("admin down iface samples = %+v", down.Hist)
	}
	if got := len(s.Snapshot()); got != 2 {
		t.Errorf("Snapshot len = %d", got)
	}

	// A counter reset must not produce a sample, but the next one should.
	poll(10*time.Second, RawCounters{InOct: 10}, RawCounters{})
	poll(15*time.Second, RawCounters{InOct: 20}, RawCounters{})
	ifc, _ = s.Get(1)
	if ifc.Hist.Len() != 2 {
		t.Fatalf("samples after reset = %d", ifc.Hist.Len())
	}

	// An interface not seen by discovery is reported as unknown.
	if !s.ApplyPoll(PollData{Status: map[int]IfStatus{3: {1, 1}}}) {
		t.Error("unknown interface not reported")
	}
}

func TestStoreReset(t *testing.T) {
	s := NewStore()
	s.UpdateInterfaces([]IfaceInfo{{Index: 1, Name: "eth0"}, {Index: 2, Name: "gone"}})
	poll := func(at time.Duration, oct uint64) {
		c := RawCounters{T: time.Unix(1000, 0).Add(at), InOct: oct, Is64Bit: true}
		s.ApplyPoll(PollData{Counters: map[int]RawCounters{1: c, 2: c}})
	}
	poll(0, 1000)
	poll(5*time.Second, 2000)
	before, _ := s.Get(1)
	s.UpdateInterfaces([]IfaceInfo{{Index: 1, Name: "eth0"}})

	s.Reset()
	if ifc, ok := s.Get(1); !ok || ifc.Name != "eth0" || ifc.Hist.Len() != 0 {
		t.Fatalf("after reset: %+v, %v", ifc, ok)
	}
	if _, ok := s.Get(2); ok {
		t.Error("interface no longer present kept after reset")
	}
	if before.Hist.Len() != 1 || before.Hist.At(0).InBps != 1600 {
		t.Error("earlier snapshot changed by reset")
	}
	// The first poll after a reset only records counters.
	poll(10*time.Second, 3000)
	if ifc, _ := s.Get(1); ifc.Hist.Len() != 0 {
		t.Errorf("samples after first poll = %d", ifc.Hist.Len())
	}
	poll(15*time.Second, 4000)
	if ifc, _ := s.Get(1); ifc.Hist.Len() != 1 {
		t.Errorf("samples after second poll = %d", ifc.Hist.Len())
	}
}

func TestPartsDelta32Wrap(t *testing.T) {
	// ucast wraps, nucast does not: summing before diffing would be wrong.
	d, ok := partsDelta([3]uint64{0xFFFFFFFF, 0xFFFFFFF0}, [3]uint64{9, 0xFFFFFFFF}, false)
	if !ok || d != 10+15 {
		t.Errorf("partsDelta = %d,%v", d, ok)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("ábc", 4); got != "ábc" {
		t.Errorf("truncate = %q", got)
	}
}

func TestFormatRateFixed(t *testing.T) {
	for _, v := range []float64{0, 7, 999, 999.96, 12345, 999949, 999951, 5e9, 123e12} {
		if got := formatRateFixed(v, "b/s"); len(got) != rateWidth {
			t.Errorf("formatRateFixed(%v) = %q, width %d", v, got, len(got))
		}
	}
}

func TestNaturalLess(t *testing.T) {
	sorted := []string{"eth0", "Eth1", "ge-0/0/2", "ge-0/0/10", "ge-0/1/0", "Gi1/0/9", "Gi1/0/10", "lo", "lo0"}
	for i := 1; i < len(sorted); i++ {
		if !naturalLess(sorted[i-1], sorted[i]) || naturalLess(sorted[i], sorted[i-1]) {
			t.Errorf("order of %q and %q", sorted[i-1], sorted[i])
		}
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port uint16
		ok   bool
	}{
		{"10.0.0.1", "10.0.0.1", 161, true},
		{"10.0.0.1:1161", "10.0.0.1", 1161, true},
		{"router1.example.com", "router1.example.com", 161, true},
		{"router1:16161", "router1", 16161, true},
		{"::1", "::1", 161, true},
		{"[::1]:1161", "::1", 1161, true},
		{"[::1]", "::1", 161, true},
		{"[router1]", "", 0, false},
		{"", "", 0, false},
		{"bad host", "", 0, false},
		{"10.0.0.1:0", "", 0, false},
		{"10.0.0.1:x", "", 0, false},
	}
	for _, c := range cases {
		h, p, err := parseTarget(c.in)
		if (err == nil) != c.ok || h != c.host || p != c.port {
			t.Errorf("parseTarget(%q) = %q, %d, %v", c.in, h, p, err)
		}
	}
}
