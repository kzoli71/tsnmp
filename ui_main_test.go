package main

import (
	"testing"
	"time"

	"github.com/rivo/tview"
)

func TestSortCycleSkipsHiddenColumns(t *testing.T) {
	a := &App{app: tview.NewApplication(), store: NewStore()}
	v := newMainView(a)

	cycle := func() []string {
		var names []string
		for i := 0; i < len(sortModes); i++ {
			v.cycleSort(1)
			names = append(names, sortModes[v.sortIdx].name)
			if v.sortIdx == 0 {
				break
			}
		}
		return names
	}
	if got := cycle(); len(got) != len(sortModes) {
		t.Errorf("all columns shown: cycle = %v", got)
	}

	v.toggle('p') // hide p/s columns
	for _, name := range cycle() {
		if name == "In p/s" || name == "Out p/s" {
			t.Errorf("p/s ordering offered while the columns are hidden")
		}
	}
	// Going backwards from ifIndex skips them too.
	v.cycleSort(-1)
	if got := sortModes[v.sortIdx].name; got != "Out%" {
		t.Errorf("S from ifIndex = %q, want Out%%", got)
	}

	// Hiding the columns while sorted by one of them resets the order.
	v.toggle('p')
	for sortModes[v.sortIdx].name != "Out p/s" {
		v.cycleSort(1)
	}
	v.toggle('p')
	if v.sortIdx != 0 {
		t.Errorf("sort by a hidden column kept: %q", sortModes[v.sortIdx].name)
	}
}

func TestSortByDescription(t *testing.T) {
	var m sortMode
	for _, sm := range sortModes {
		if sm.column == "Description" {
			m = sm
		}
	}
	a := &Iface{Alias: "uplink 10"}
	b := &Iface{Alias: "uplink 9"}
	c := &Iface{Descr: "access"} // no alias: ifDescr is the description
	if !m.less(b, a) || !m.less(c, b) || m.less(a, c) {
		t.Error("description order should be: access, uplink 9, uplink 10")
	}
}

func TestFindOverSpeed(t *testing.T) {
	mk := func(name string, speed uint64, in, out float64) Iface {
		ifc := Iface{Name: name, Speed: speed}
		ifc.Hist.append(Sample{T: time.Now(), InBps: in, OutBps: out})
		return ifc
	}
	ifs := []Iface{
		mk("ok", 10e9, 8e9, 7e9),
		mk("nospeed", 0, 50e9, 0), // unknown speed: never flagged
		mk("over", 10e9, 9e9, 10.7e9),
		mk("lessover", 1e9, 1.01e9, 0),
	}
	ev, ok := findOverSpeed(ifs)
	if !ok || ev.name != "over" || ev.rate < 10.69e9 || ev.rate > 10.71e9 { // float32 storage
		t.Errorf("findOverSpeed = %+v, %v", ev, ok)
	}
	if _, ok := findOverSpeed(ifs[:2]); ok {
		t.Error("flagged without any rate above speed")
	}
}
