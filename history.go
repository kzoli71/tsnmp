package main

import "time"

// Samples are stored packed (20 bytes instead of 56 for Sample, and without
// pointers, so the garbage collector does not scan them) in fixed-size
// blocks, so the history never gets copied or over-allocated as it grows.

const blockLen = 512 // samples per block (10 KB)

// historyTick is the time resolution of stored samples; uint32 ticks of
// 100 ms last about 13 years from the first sample.
const historyTick = 100 * time.Millisecond

type packedSample struct {
	t                            uint32 // historyTicks since History.base
	inBps, outBps, inPps, outPps float32
}

type historyBlock [blockLen]packedSample

// History is an append-only sample list. A copy of the value is a
// consistent read-only snapshot: the owner only ever writes past the copy's
// length, so it can be read without the store lock.
type History struct {
	base   time.Time
	blocks []*historyBlock
	n      int
}

func (h *History) append(s Sample) {
	if h.n == 0 {
		h.base = s.T
	}
	if h.n%blockLen == 0 {
		h.blocks = append(h.blocks, new(historyBlock))
	}
	h.blocks[h.n/blockLen][h.n%blockLen] = packedSample{
		t:     uint32(max(s.T.Sub(h.base), 0) / historyTick),
		inBps: float32(s.InBps), outBps: float32(s.OutBps),
		inPps: float32(s.InPps), outPps: float32(s.OutPps),
	}
	h.n++
}

func (h History) Len() int { return h.n }

// At returns sample i (0 = oldest).
func (h History) At(i int) Sample {
	p := h.blocks[i/blockLen][i%blockLen]
	return Sample{
		T:     h.base.Add(time.Duration(p.t) * historyTick),
		InBps: float64(p.inBps), OutBps: float64(p.outBps),
		InPps: float64(p.inPps), OutPps: float64(p.outPps),
	}
}

// Range returns samples [from, to) unpacked.
func (h History) Range(from, to int) []Sample {
	out := make([]Sample, 0, max(to-from, 0))
	for i := from; i < to; i++ {
		out = append(out, h.At(i))
	}
	return out
}

// Last returns the newest sample, if any.
func (h History) Last() (Sample, bool) {
	if h.n == 0 {
		return Sample{}, false
	}
	return h.At(h.n - 1), true
}
