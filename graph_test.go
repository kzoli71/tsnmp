package main

import (
	"fmt"
	"testing"
	"time"
)

// histN returns a History of n samples one second apart, InBps = index.
func histN(n int) History {
	var h History
	for _, s := range samplesN(n) {
		h.append(s)
	}
	return h
}

func samplesN(n int) []Sample {
	out := make([]Sample, n)
	t0 := time.Unix(0, 0)
	for i := range out {
		out[i] = Sample{T: t0.Add(time.Duration(i) * time.Second), InBps: float64(i)}
	}
	return out
}

func TestViewRange(t *testing.T) {
	cases := []struct{ points, capacity, offset, start, end, off int }{
		{10, 50, 0, 0, 10, 0},    // fewer points than space
		{10, 50, 5, 0, 10, 0},    // cannot scroll when everything fits
		{100, 40, 0, 60, 100, 0}, // live
		{100, 40, 10, 50, 90, 10},
		{100, 40, 999, 0, 40, 60}, // clamped to the oldest page
		{100, 40, -3, 60, 100, 0},
	}
	for _, c := range cases {
		s, e, o := viewRange(c.points, c.capacity, c.offset)
		if s != c.start || e != c.end || o != c.off {
			t.Errorf("viewRange(%d,%d,%d) = %d,%d,%d want %d,%d,%d",
				c.points, c.capacity, c.offset, s, e, o, c.start, c.end, c.off)
		}
	}
}

func TestAggregate(t *testing.T) {
	smp := samplesN(12) // InBps 0..11
	pts := aggregate(smp, 5, 0, numPoints(len(smp), 5))
	if len(pts) != 3 {
		t.Fatalf("points = %d", len(pts))
	}
	// buckets: 0-4 avg 2, 5-9 avg 7, 10-11 avg 10.5 (partial)
	for i, want := range []float64{2, 7, 10.5} {
		if pts[i].InBps != want {
			t.Errorf("point %d = %v want %v", i, pts[i].InBps, want)
		}
	}
	if !pts[2].T.Equal(smp[11].T) {
		t.Errorf("partial bucket time = %v", pts[2].T)
	}
}

func TestGraphScrollKeepsViewFixed(t *testing.T) {
	a := &App{store: NewStore()}
	a.store.UpdateInterfaces([]IfaceInfo{{Index: 1, Admin: 1, Oper: 1}})
	ifc := a.store.ifaces[1]
	ifc.Hist = histN(100)
	g := newGraphView(a)
	g.show(1)
	g.capacity = 40

	g.scroll(10)
	if g.offset != 10 {
		t.Fatalf("offset = %d", g.offset)
	}
	// Five new samples arrive: the same points must stay in view.
	ifc.Hist = histN(105)
	g.refresh()
	if s, e, _ := viewRange(g.lastPoints, g.capacity, g.offset); s != 50 || e != 90 {
		t.Errorf("view after new samples = %d..%d, want 50..90", s, e)
	}
	g.scroll(1000) // End
	if g.offset != 65 {
		t.Errorf("End offset = %d, want 65", g.offset)
	}
	g.scroll(-1000) // back to live
	if g.offset != 0 {
		t.Errorf("offset after scrolling forward = %d", g.offset)
	}
	ifc.Hist = histN(110)
	g.refresh()
	if g.offset != 0 {
		t.Errorf("live view moved: offset = %d", g.offset)
	}
}

func TestGraphZoomKeepsRightEdge(t *testing.T) {
	a := &App{store: NewStore()}
	a.store.UpdateInterfaces([]IfaceInfo{{Index: 1, Admin: 1, Oper: 1}})
	a.store.ifaces[1].Hist = histN(105)
	g := newGraphView(a)
	g.show(1)
	g.capacity = 10
	g.scroll(55) // right edge at sample 50
	g.setZoom(1) // 5x
	if _, e, _ := viewRange(g.lastPoints, g.capacity, g.offset); e*5 != 50 {
		t.Errorf("zoomed right edge = sample %d, want 50", e*5)
	}
	g.setZoom(0)
	if _, e, _ := viewRange(g.lastPoints, g.capacity, g.offset); e != 50 {
		t.Errorf("unzoomed right edge = %d, want 50", e)
	}
}

func TestTimeTicks(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	pts := make([]Sample, 30)
	for i := range pts {
		pts[i].T = now.Add(time.Duration(i-29) * 5 * time.Second)
	}
	// Chart wider than the data: points occupy the last 30 columns.
	ticks := timeTicks(pts, 60, now)
	if len(ticks) != 3 {
		t.Fatalf("ticks = %+v", ticks)
	}
	if ticks[0].col != 59 || ticks[0].text != "12:00:00" || ticks[0].labelX != 52 {
		t.Errorf("newest tick = %+v", ticks[0])
	}
	// Each label shows the time of the point in its column.
	for _, tk := range ticks {
		if want := pts[tk.col-30].T.Format("15:04:05"); tk.text != want {
			t.Errorf("tick at col %d = %s want %s", tk.col, tk.text, want)
		}
		if tk.labelX < 0 || tk.labelX+len(tk.text) > 60 {
			t.Errorf("label out of chart: %+v", tk)
		}
	}
	for i := 1; i < len(ticks); i++ {
		if ticks[i].labelX+len(ticks[i].text) >= ticks[i-1].labelX {
			t.Errorf("labels overlap: %+v %+v", ticks[i], ticks[i-1])
		}
	}

	// Data from another day gets the date in the label.
	if tk := timeTicks(pts, 60, now.Add(24*time.Hour)); len(tk) == 0 || tk[0].text != "10-05 12:00" {
		t.Errorf("dated ticks = %+v", tk)
	}
	if timeTicks(nil, 60, now) != nil {
		t.Error("ticks without points")
	}
}

func TestGraphZoomCycles(t *testing.T) {
	a := &App{store: NewStore()}
	a.store.UpdateInterfaces([]IfaceInfo{{Index: 1, Admin: 1, Oper: 1}})
	a.store.ifaces[1].Hist = histN(10)
	g := newGraphView(a)
	g.show(1)
	var got []int
	for i := 0; i < 5; i++ {
		g.setZoom(g.zoomIdx + 1)
		got = append(got, g.zoom())
	}
	if want := []int{5, 30, 120, 1, 5}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("z sequence = %v want %v", got, want)
	}
	g.setZoom(0)
	g.setZoom(g.zoomIdx - 1)
	if g.zoom() != 120 {
		t.Errorf("Z from 1x = %d×, want 120×", g.zoom())
	}
}

func TestRecentOverSpeed(t *testing.T) {
	t0 := time.Unix(1000, 0)
	ifc := Iface{Speed: 1000}
	ifc.Hist.append(Sample{T: t0, InBps: 2000}) // over speed
	ifc.Hist.append(Sample{T: t0.Add(30 * time.Second), OutBps: 500})
	if !recentOverSpeed(&ifc, t0.Add(40*time.Second)) {
		t.Error("recent over-speed sample not reported")
	}
	if recentOverSpeed(&ifc, t0.Add(overSpeedShow+time.Second)) {
		t.Error("old over-speed sample still reported")
	}
	ifc.Speed = 0
	if recentOverSpeed(&ifc, t0.Add(time.Second)) {
		t.Error("reported with unknown speed")
	}
}

func TestFitVariant(t *testing.T) {
	for _, w := range []int{200, 120, 80, 50, 30, 10} {
		got := fitVariant(overSpeedWarnings, w)
		if n := len([]rune(got)) + 2; n > w && got != overSpeedWarnings[len(overSpeedWarnings)-1] {
			t.Errorf("width %d: %q does not fit", w, got)
		}
	}
	if got := fitVariant(overSpeedWarnings, 80); got != overSpeedWarnings[1] {
		t.Errorf("width 80: %q", got)
	}
}

func TestCellRuneFallback(t *testing.T) {
	defer func(v bool) { noBraille = v }(noBraille)
	noBraille = false
	if r := cellRune(0x41); r != 0x2841 {
		t.Fatalf("braille: got %U", r)
	}
	noBraille = true
	if r := cellRune(0x01); r != '█' {
		t.Fatalf("fallback: got %c", r)
	}
}

func TestGraphWheelZoomStopsAtEnds(t *testing.T) {
	a := &App{store: NewStore()}
	a.store.UpdateInterfaces([]IfaceInfo{{Index: 1, Admin: 1, Oper: 1}})
	a.store.ifaces[1].Hist = histN(10)
	g := newGraphView(a)
	g.show(1)
	g.wheelZoom(-1)
	if g.zoom() != 1 {
		t.Errorf("zoom in at 1× gave %d×", g.zoom())
	}
	for i := 0; i < 5; i++ {
		g.wheelZoom(1)
	}
	if g.zoom() != 120 {
		t.Errorf("zoom out stopped at %d×, want 120×", g.zoom())
	}
	g.wheelZoom(-1)
	if g.zoom() != 30 {
		t.Errorf("zoom in from 120× gave %d×, want 30×", g.zoom())
	}
}
