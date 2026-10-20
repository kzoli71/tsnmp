package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var (
	colorIn   = tcell.ColorGreen
	colorOut  = tcell.ColorDodgerBlue
	colorBoth = tcell.ColorYellow
	colorAxis = tcell.ColorGray
)

// graphStyle is the base style of everything drawn on the graph page. The
// background must match the one tview fills the box with; tcell.StyleDefault
// would show the terminal's own background (e.g. aubergine on Ubuntu) behind
// every drawn character.
func graphStyle() tcell.Style {
	return tcell.StyleDefault.Background(tview.Styles.PrimitiveBackgroundColor)
}

const (
	yLabelWidth   = 13
	dotsPerSample = 2 // horizontal braille dots between consecutive samples
)

// zoomLevels is the number of samples averaged into one plotted point.
var zoomLevels = []int{1, 5, 30, 120}

type GraphView struct {
	a       *App
	root    *tview.Box
	ifIndex int

	// offset is how many points the view is scrolled back from the newest
	// one; 0 means live. While scrolled back the view stays on the same
	// points as new samples arrive.
	offset     int
	zoomIdx    int
	lastPoints int  // point count at the last refresh, to keep the view fixed
	capacity   int  // points that fit the chart width at the last draw
	overSpeed  bool // a recent sample exceeded the interface speed
}

func newGraphView(a *App) *GraphView {
	g := &GraphView{a: a, capacity: 1}
	g.root = tview.NewBox().SetBorder(true)
	g.root.SetDrawFunc(func(screen tcell.Screen, x, y, w, h int) (int, int, int, int) {
		if ifc, ok := g.a.store.Get(g.ifIndex); ok {
			t := g.title(&ifc, w-2)
			printText(screen, x+(w-len([]rune(t)))/2, y, t, graphStyle())
		}
		g.draw(screen, x+1, y+1, w-2, h-2)
		// Warnings on the bottom border; a connection error comes first.
		var warn []string
		color := tcell.ColorYellow
		switch {
		case g.a.stale():
			warn, color = []string{g.a.staleText()}, tcell.ColorRed
		case g.overSpeed:
			warn = overSpeedWarnings
		}
		if len(warn) > 0 {
			t := " " + fitVariant(warn, w-2) + " "
			printClipped(screen, x+max((w-len([]rune(t)))/2, 1), y+h-1, x+w-1, t,
				graphStyle().Foreground(color).Bold(true))
		}
		return x + 1, y + 1, w - 2, h - 2
	})
	// Right click goes back to the list, the wheel zooms (unless a dialog is
	// open on top).
	g.root.SetMouseCapture(func(action tview.MouseAction, ev *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if page, _ := a.pages.GetFrontPage(); page != "graph" {
			return action, ev
		}
		switch action {
		case tview.MouseRightClick:
			a.closeGraph()
		case tview.MouseScrollUp:
			g.wheelZoom(-1)
		case tview.MouseScrollDown:
			g.wheelZoom(1)
		default:
			return action, ev
		}
		return action, nil
	})
	return g
}

func (g *GraphView) zoom() int { return zoomLevels[g.zoomIdx] }

// show opens the graph of an interface in live view (zoom is kept).
func (g *GraphView) show(ifIndex int) {
	g.ifIndex = ifIndex
	g.offset = 0
	g.lastPoints = 0
	g.refresh()
}

// refresh is called after every poll.
func (g *GraphView) refresh() {
	ifc, ok := g.a.store.Get(g.ifIndex)
	if !ok {
		return
	}
	points := numPoints(ifc.Hist.Len(), g.zoom())
	if g.offset > 0 {
		g.offset += points - g.lastPoints
	}
	g.lastPoints = points
}

// title returns the box title fitted to width: the description is shortened
// first, then the key hints, so the host and interface name stay visible.
func (g *GraphView) title(ifc *Iface, width int) string {
	host := g.a.sysName
	if host == "" {
		host = g.a.host
	}
	poll := int(g.a.interval.Seconds())
	descr := ifc.Description()
	for _, hints := range []string{fmt.Sprintf("poll %ds, F1: help, Esc: back", poll), fmt.Sprintf("poll %ds", poll)} {
		base := fmt.Sprintf(" %s: %s  (%s) ", host, ifc.Name, hints)
		if room := width - len([]rune(base)) - 3; descr != "" && room >= 8 {
			return fmt.Sprintf(" %s: %s – %s  (%s) ", host, ifc.Name, truncate(descr, min(room, 40)), hints)
		}
		if len([]rune(base)) <= width {
			return base
		}
	}
	return truncate(fmt.Sprintf(" %s: %s ", host, ifc.Name), width)
}

func (g *GraphView) scroll(delta int) {
	g.offset = min(max(g.offset+delta, 0), max(g.lastPoints-g.capacity, 0))
}

// setZoom changes the zoom level, keeping the right edge of a scrolled view
// at about the same time. The level index wraps around (120× -> 1× and back).
func (g *GraphView) setZoom(idx int) {
	idx = (idx%len(zoomLevels) + len(zoomLevels)) % len(zoomLevels)
	ifc, ok := g.a.store.Get(g.ifIndex)
	if !ok || idx == g.zoomIdx {
		return
	}
	n := ifc.Hist.Len()
	oldZ, newZ := g.zoom(), zoomLevels[idx]
	g.zoomIdx = idx
	newPoints := numPoints(n, newZ)
	if g.offset > 0 {
		endSample := min((numPoints(n, oldZ)-g.offset)*oldZ, n)
		g.offset = max(newPoints-numPoints(endSample, newZ), 0)
	}
	g.lastPoints = newPoints
}

// wheelZoom zooms in (-1) or out (+1) by one level; unlike z / Z it stops at
// the ends instead of wrapping around.
func (g *GraphView) wheelZoom(step int) {
	if idx := g.zoomIdx + step; idx >= 0 && idx < len(zoomLevels) {
		g.setZoom(idx)
	}
}

// handleKey processes graph navigation keys; it returns false for other keys.
func (g *GraphView) handleKey(ev *tcell.EventKey) bool {
	page := max(g.capacity-1, 1)
	half := max(g.capacity/2, 1)
	switch {
	case ev.Key() == tcell.KeyLeft && ev.Modifiers()&tcell.ModShift != 0:
		g.scroll(half)
	case ev.Key() == tcell.KeyRight && ev.Modifiers()&tcell.ModShift != 0:
		g.scroll(-half)
	case ev.Key() == tcell.KeyLeft:
		g.scroll(1)
	case ev.Key() == tcell.KeyRight:
		g.scroll(-1)
	case ev.Key() == tcell.KeyPgUp:
		g.scroll(page)
	case ev.Key() == tcell.KeyPgDn:
		g.scroll(-page)
	case ev.Key() == tcell.KeyHome:
		g.offset = 0
	case ev.Key() == tcell.KeyEnd:
		g.scroll(g.lastPoints)
	case ev.Rune() == 'z':
		g.setZoom(g.zoomIdx + 1)
	case ev.Rune() == 'Z':
		g.setZoom(g.zoomIdx - 1)
	case ev.Rune() == 'w' || ev.Rune() == 'W':
		g.a.saveCSV(g.ifIndex)
	default:
		return false
	}
	return true
}

// numPoints is the number of plotted points n samples give at zoom z.
func numPoints(n, z int) int { return (n + z - 1) / z }

// viewRange returns the points [start, end) to plot and the offset clamped
// to the available history.
func viewRange(points, capacity, offset int) (start, end, off int) {
	off = min(max(offset, 0), max(points-capacity, 0))
	end = points - off
	start = max(end-capacity, 0)
	return start, end, off
}

// aggregate returns points [from, to), where point k is the average of
// samples[k*z : (k+1)*z] (the last one may be partial) timed at its last sample.
func aggregate(samples []Sample, z, from, to int) []Sample {
	out := make([]Sample, 0, max(to-from, 0))
	for k := from; k < to; k++ {
		b := samples[k*z : min((k+1)*z, len(samples))]
		var p Sample
		for _, s := range b {
			p.InBps += s.InBps
			p.OutBps += s.OutBps
			p.InPps += s.InPps
			p.OutPps += s.OutPps
		}
		n := float64(len(b))
		p.InBps, p.OutBps, p.InPps, p.OutPps = p.InBps/n, p.OutBps/n, p.InPps/n, p.OutPps/n
		p.T = b[len(b)-1].T
		out = append(out, p)
	}
	return out
}

// overSpeedWarnings are the graph's over-speed warning texts, longest first;
// the longest that fits the width is shown, so the advice is not cut off.
var overSpeedWarnings = []string{
	"⚠ the rate exceeded the interface speed: the counters are updated less often than the poll interval – increase the poll interval",
	"⚠ the rate exceeded the interface speed – increase the poll interval",
	"⚠ rate > speed – increase the poll interval",
	"⚠ rate > speed",
}

// fitVariant returns the first text that fits width with a space on both
// sides, or the last one (then clipped by the caller).
func fitVariant(texts []string, width int) string {
	for _, t := range texts {
		if len([]rune(t))+2 <= width {
			return t
		}
	}
	return texts[len(texts)-1]
}

// recentOverSpeed reports whether a sample within overSpeedShow before now
// exceeded the interface speed. Like the status line warning it is advice
// for the current poll interval, so older samples (even in view) do not
// keep it on.
func recentOverSpeed(ifc *Iface, now time.Time) bool {
	if ifc.Speed == 0 {
		return false
	}
	for i := ifc.Hist.Len() - 1; i >= 0; i-- {
		s := ifc.Hist.At(i)
		if now.Sub(s.T) >= overSpeedShow {
			return false
		}
		if max(s.InBps, s.OutBps) > float64(ifc.Speed) {
			return true
		}
	}
	return false
}

// graphWindow is what both panels plot.
type graphWindow struct {
	points []Sample // aggregated, one per plotted point
	raw    []Sample // the samples behind them, for current/min/max
	live   bool
	zoom   int
}

func (g *GraphView) draw(screen tcell.Screen, x, y, w, h int) {
	g.overSpeed = false
	ifc, ok := g.a.store.Get(g.ifIndex)
	if !ok || w < yLabelWidth+10 || h < 8 {
		return
	}
	// Newest point at the right edge, dotsPerSample dots apart.
	chartW := w - yLabelWidth
	g.capacity = (chartW*2-1)/dotsPerSample + 1
	z := g.zoom()
	n := ifc.Hist.Len()
	start, end, off := viewRange(numPoints(n, z), g.capacity, g.offset)
	g.offset = off
	// Only the samples in view are unpacked; start*z is a bucket boundary.
	raw := ifc.Hist.Range(start*z, min(end*z, n))
	g.overSpeed = recentOverSpeed(&ifc, time.Now())
	win := graphWindow{
		points: aggregate(raw, z, 0, end-start),
		raw:    raw,
		live:   off == 0,
		zoom:   z,
	}

	half := h / 2
	g.drawPanel(screen, x, y, w, half, "Bit/s", "b/s", win,
		func(s Sample) (float64, float64) { return s.InBps, s.OutBps })
	g.drawPanel(screen, x, y+half, w, h-half, "Packet/s", "p/s", win,
		func(s Sample) (float64, float64) { return s.InPps, s.OutPps })
}

// printClipped is printText that stops before column right.
func printClipped(screen tcell.Screen, x, y, right int, s string, st tcell.Style) int {
	for _, r := range s {
		if x >= right {
			break
		}
		screen.SetContent(x, y, r, nil, st)
		x++
	}
	return x
}

func printText(screen tcell.Screen, x, y int, s string, st tcell.Style) int {
	for _, r := range s {
		screen.SetContent(x, y, r, nil, st)
		x++
	}
	return x
}

// drawPanel draws one braille line chart with title, legend and axes.
func (g *GraphView) drawPanel(screen tcell.Screen, x, y, w, h int, title, unit string,
	win graphWindow, get func(Sample) (float64, float64)) {

	def := graphStyle()
	chartX := x + yLabelWidth
	chartW := w - yLabelWidth
	if chartW < 2 {
		return
	}

	dotsW := chartW * 2
	window := win.points

	// Legend with the newest shown value and min/max of the raw samples in
	// the view (so short peaks are not averaged away when zoomed out).
	legend := func(name string, sel func(Sample) float64) string {
		if len(win.raw) == 0 {
			return "● " + name + ": -"
		}
		cur := sel(win.raw[len(win.raw)-1])
		lo, hi := cur, cur
		for _, s := range win.raw {
			lo, hi = math.Min(lo, sel(s)), math.Max(hi, sel(s))
		}
		return fmt.Sprintf("● %s: %s  (min %s, max %s)", name,
			formatRate(cur, unit), formatRate(lo, unit), formatRate(hi, unit))
	}
	head := title + "  "
	inText := legend("In", func(s Sample) float64 { in, _ := get(s); return in })
	outText := legend("Out", func(s Sample) float64 { _, out := get(s); return out })
	right := x + w
	legendRows := 1
	cx := printClipped(screen, x, y, right, head, def.Bold(true))
	cx = printClipped(screen, cx, y, right, inText+"   ", def.Foreground(colorIn))
	if cx+len([]rune(outText)) > right {
		// Too narrow for one line: Out goes below In.
		legendRows = 2
		printClipped(screen, x+len([]rune(head)), y+1, right, outText, def.Foreground(colorOut))
	} else {
		printClipped(screen, cx, y, right, outText, def.Foreground(colorOut))
	}

	chartY := y + legendRows
	chartH := h - legendRows - 2 // legend, x-axis line, x label
	if chartH < 2 {
		return
	}
	dotsH := chartH * 4

	peak := 0.0
	for _, s := range window {
		in, out := get(s)
		peak = math.Max(peak, math.Max(in, out))
	}
	scale := niceMax(peak)

	// Y axis labels and line.
	axis := def.Foreground(colorAxis)
	for row := 0; row < chartH; row++ {
		screen.SetContent(chartX-1, chartY+row, '│', nil, axis)
	}
	printLabel := func(row int, v float64) {
		lbl := formatRate(v, unit)
		printText(screen, chartX-2-len([]rune(lbl)), chartY+row, lbl, axis)
	}
	printLabel(0, scale)
	if chartH >= 4 { // otherwise the middle label would overwrite another
		printLabel(chartH/2, scale/2)
	}
	printLabel(chartH-1, 0)

	// X axis with tick marks under the labelled points.
	axisY, labelY := chartY+chartH, chartY+chartH+1
	screen.SetContent(chartX-1, axisY, '└', nil, axis)
	for col := 0; col < chartW; col++ {
		screen.SetContent(chartX+col, axisY, '─', nil, axis)
	}
	ticks := timeTicks(window, chartW, time.Now())
	for _, t := range ticks {
		screen.SetContent(chartX+t.col, axisY, '┴', nil, axis)
		printText(screen, chartX+t.labelX, labelY, t.text, axis)
	}
	if len(ticks) == 0 {
		printText(screen, chartX, labelY, "Collecting data...", axis)
	}

	// Zoom, time span and live/history mode on the axis line: the longest
	// variant that fits.
	span := ""
	if len(win.raw) > 1 {
		span = formatDuration(win.raw[len(win.raw)-1].T.Sub(win.raw[0].T))
	}
	variants, modeStyle := []string{
		fmt.Sprintf(" zoom %d× · ← %s ", win.zoom, span),
		fmt.Sprintf(" %d× · %s ", win.zoom, span),
	}, axis
	if !win.live {
		variants = []string{
			fmt.Sprintf(" zoom %d× · %s · ◀ HISTORY – Home: live ", win.zoom, span),
			fmt.Sprintf(" %d× · ◀ HISTORY ", win.zoom),
			" ◀ HIST. ",
		}
		modeStyle = def.Foreground(colorBoth)
	}
	for _, m := range variants {
		if len([]rune(m)) <= chartW-2 {
			printText(screen, chartX+1, axisY, m, modeStyle)
			break
		}
	}

	// Plot both series into braille bitmaps.
	inMask := make([]uint8, chartW*chartH)
	outMask := make([]uint8, chartW*chartH)
	offset := dotsW - 1 - (len(window)-1)*dotsPerSample // right-align
	toDot := func(v float64) int {
		d := int(math.Round(v / scale * float64(dotsH-1)))
		return min(max(d, 0), dotsH-1)
	}
	plot := func(mask []uint8, sel func(Sample) float64) {
		prevX, prevY := -1, 0
		for i, s := range window {
			x1, y1 := offset+i*dotsPerSample, toDot(sel(s))
			if prevX < 0 {
				setDot(mask, chartW, chartH, x1, y1)
			}
			// Interpolate from the previous point, filling vertical gaps.
			for x := prevX + 1; prevX >= 0 && x <= x1; x++ {
				f := float64(x-prevX) / float64(x1-prevX)
				ya := int(math.Round(float64(prevY) + f*float64(y1-prevY)))
				yb := int(math.Round(float64(prevY) + (f-1/float64(x1-prevX))*float64(y1-prevY)))
				for d := min(ya, yb); d <= max(ya, yb); d++ {
					setDot(mask, chartW, chartH, x, d)
				}
			}
			prevX, prevY = x1, y1
		}
	}
	plot(inMask, func(s Sample) float64 { in, _ := get(s); return in })
	plot(outMask, func(s Sample) float64 { _, out := get(s); return out })

	for row := 0; row < chartH; row++ {
		for col := 0; col < chartW; col++ {
			i := row*chartW + col
			m := inMask[i] | outMask[i]
			if m == 0 {
				continue
			}
			color := colorIn
			switch {
			case inMask[i] != 0 && outMask[i] != 0:
				color = colorBoth
			case outMask[i] != 0:
				color = colorOut
			}
			screen.SetContent(chartX+col, chartY+row, cellRune(m), nil, def.Foreground(color))
		}
	}
}

// tick is one x-axis time label.
type tick struct {
	col    int // chart column of the labelled point (tick mark)
	labelX int // chart column where the label text starts
	text   string
}

// timeTicks labels every few columns of a chart whose points are plotted
// one per column, right-aligned (newest in the last column). Labels show
// the time of the point above them; the date is added when the points are
// not all from today.
func timeTicks(points []Sample, chartW int, now time.Time) []tick {
	n := len(points)
	if n == 0 || chartW <= 0 {
		return nil
	}
	layout := "15:04:05"
	y, m, d := now.Date()
	for _, p := range []Sample{points[0], points[n-1]} {
		if py, pm, pd := p.T.Date(); py != y || pm != m || pd != d {
			layout = "01-02 15:04"
		}
	}
	lw := len(layout)
	step := lw + 6
	var out []tick
	nextFree := chartW // labels are placed right to left
	for c := chartW - 1; c >= chartW-n && c >= 0; c -= step {
		lx := min(max(c-lw/2, 0), chartW-lw)
		if lx < 0 || lx+lw+2 > nextFree && len(out) > 0 {
			break // no room left without overlapping
		}
		text := points[c-(chartW-n)].T.Format(layout)
		out = append(out, tick{col: c, labelX: lx, text: text})
		nextFree = lx
	}
	return out
}

// Braille dot bits indexed by [column][row within the cell, top to bottom].
var brailleBits = [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

// noBraille is set on the Linux virtual console: its fonts have no braille
// glyphs, it would show substitutes (◆, letters) instead.
var noBraille = os.Getenv("TERM") == "linux"

// cellRune returns the character for a braille dot mask: the braille
// pattern itself, or on a terminal without braille glyphs a full block. The
// console fonts (console-setup Fixed, Terminus, VGA) have █ but no half
// blocks (▀ ▄), those are shown as '#'.
func cellRune(m uint8) rune {
	if noBraille {
		return '█'
	}
	return rune(0x2800 + int(m))
}

// setDot sets a dot; dy counts from the bottom of the chart.
func setDot(mask []uint8, w, h, dx, dy int) {
	if dx < 0 || dx >= w*2 || dy < 0 || dy >= h*4 {
		return
	}
	fromTop := h*4 - 1 - dy
	mask[(fromTop/4)*w+dx/2] |= brailleBits[dx%2][fromTop%4]
}

// niceMax rounds v up to 1, 2 or 5 times a power of ten.
func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}
