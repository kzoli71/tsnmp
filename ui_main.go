package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	maxNameLen  = 24
	maxDescrLen = 30
	minDescrLen = 10 // the description shrinks down to this on narrow screens
	pctWidth    = 5  // width of the In% / Out% cells ("100.0")
)

// sortMode is one table ordering; key returns the sort value of an interface
// (rates: descending, higher first).
type sortMode struct {
	name   string
	column string // header of the sorted column, marked with ▼/▲
	less   func(a, b *Iface) bool
}

// rateLess orders by a rate, highest first; interfaces without data last.
func rateLess(get func(Sample) float64) func(a, b *Iface) bool {
	val := func(ifc *Iface) float64 {
		if s, ok := ifc.Latest(); ok {
			return get(s)
		}
		return -1
	}
	return func(a, b *Iface) bool { return val(a) > val(b) }
}

// pctLess orders by utilization (rate / speed), highest first; interfaces
// without speed or data last.
func pctLess(get func(Sample) float64) func(a, b *Iface) bool {
	val := func(ifc *Iface) float64 {
		if s, ok := ifc.Latest(); ok && ifc.Speed > 0 {
			return get(s) / float64(ifc.Speed)
		}
		return -1
	}
	return func(a, b *Iface) bool { return val(a) > val(b) }
}

var sortModes = []sortMode{
	{"ifIndex", "", func(a, b *Iface) bool { return a.Index < b.Index }},
	{"name", "Name", func(a, b *Iface) bool { return naturalLess(a.Name, b.Name) }},
	{"description", "Description", func(a, b *Iface) bool { return naturalLess(a.Description(), b.Description()) }},
	{"In b/s", "In b/s", rateLess(func(s Sample) float64 { return s.InBps })},
	{"Out b/s", "Out b/s", rateLess(func(s Sample) float64 { return s.OutBps })},
	{"In%", "In%", pctLess(func(s Sample) float64 { return s.InBps })},
	{"Out%", "Out%", pctLess(func(s Sample) float64 { return s.OutBps })},
	{"In p/s", "In p/s", rateLess(func(s Sample) float64 { return s.InPps })},
	{"Out p/s", "Out p/s", rateLess(func(s Sample) float64 { return s.OutPps })},
}

type MainView struct {
	a        *App
	root     *tview.Flex
	hostInfo *tview.TextView
	interval *tview.TextView
	table    *tview.Table
	bottom   *tview.Pages // "status" or "search"
	status   *tview.TextView
	search   *tview.InputField

	rowIndex []int // table row (minus header) -> ifIndex
	selected int   // ifIndex of the selected row

	// display filters, toggled with a / o / i / p
	showAdminDown bool
	showOperDown  bool
	showIdx       bool
	showPackets   bool
	sortIdx       int    // index into sortModes, cycled with s / S
	query         string // search text (name or description), set with /
}

func newMainView(a *App) *MainView {
	v := &MainView{a: a, selected: -1, showOperDown: true, showPackets: true}

	v.hostInfo = tview.NewTextView().SetDynamicColors(true)
	v.interval = tview.NewTextView().SetTextAlign(tview.AlignCenter)
	minus := tview.NewButton("-").SetSelectedFunc(func() {
		a.changeInterval(false)
		a.app.SetFocus(v.table)
	})
	plus := tview.NewButton("+").SetSelectedFunc(func() {
		a.changeInterval(true)
		a.app.SetFocus(v.table)
	})
	help := tview.NewButton("F1 Help").SetSelectedFunc(func() {
		a.app.SetFocus(v.table) // so closing the help returns to the table
		a.showHelp()
	})
	quit := tview.NewButton("Quit").SetSelectedFunc(func() {
		a.app.SetFocus(v.table)
		a.confirmQuit()
	})
	for _, b := range []*tview.Button{minus, plus} {
		// Rapid clicks arrive as double clicks; count them as clicks too.
		b.SetMouseCapture(func(action tview.MouseAction, ev *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
			if action == tview.MouseLeftDoubleClick {
				action = tview.MouseLeftClick
			}
			return action, ev
		})
	}

	header := tview.NewFlex().
		AddItem(v.hostInfo, 0, 1, false).
		AddItem(tview.NewTextView().SetText("Poll:"), 6, 0, false).
		AddItem(minus, 3, 0, false).
		AddItem(v.interval, 6, 0, false).
		AddItem(plus, 3, 0, false).
		AddItem(nil, 3, 0, false).
		AddItem(help, 9, 0, false).
		AddItem(nil, 2, 0, false).
		AddItem(quit, 6, 0, false)

	v.table = tview.NewTable().
		SetFixed(1, 0).
		SetSelectable(true, false).
		SetSeparator(' ')
	v.table.SetBorder(true)
	v.table.SetSelectedFunc(func(row, _ int) {
		if idx, ok := v.ifIndexAt(row); ok {
			a.openGraph(idx)
		}
	})
	v.table.SetSelectionChangedFunc(func(row, _ int) {
		if idx, ok := v.ifIndexAt(row); ok {
			v.selected = idx
		}
	})

	v.status = tview.NewTextView().SetDynamicColors(true)
	v.search = tview.NewInputField().SetLabel("Search (name / description): ")
	v.search.SetChangedFunc(func(text string) {
		v.query = text
		v.renderTable()
	})
	v.search.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			v.search.SetText("") // also clears v.query via the changed func
		}
		v.bottom.SwitchToPage("status")
		a.app.SetFocus(v.table)
	})
	v.bottom = tview.NewPages().
		AddPage("status", v.status, true, true).
		AddPage("search", v.search, true, false)

	v.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(v.table, 0, 1, true).
		AddItem(v.bottom, 1, 0, false)

	v.updateHeader()
	v.renderTable()
	v.status.SetText("[yellow]First poll...")
	return v
}

func (v *MainView) ifIndexAt(row int) (int, bool) {
	i := row - 1
	if i < 0 || i >= len(v.rowIndex) {
		return 0, false
	}
	return v.rowIndex[i], true
}

// headerRightWidth is the width of the fixed header items right of the host
// info (poll interval, help and quit buttons; see newMainView).
const headerRightWidth = 6 + 3 + 6 + 3 + 3 + 9 + 2 + 6

// updateHeader shows the host, its name and the SNMP settings: in full,
// shortened or left out, whichever fits the screen width.
func (v *MainView) updateHeader() {
	plain := fmt.Sprintf("Host: %s  Name: %s", v.a.host, v.a.sysName)
	text := fmt.Sprintf("Host: [::b]%s[::-]  Name: [::b]%s[::-]", tview.Escape(v.a.host), tview.Escape(v.a.sysName))
	for _, d := range []string{v.a.snmp.Describe(), v.a.snmp.Short()} {
		if len([]rune(plain+"  SNMP "+d)) < v.a.screenW-headerRightWidth {
			text += "  SNMP " + tview.Escape(d)
			break
		}
	}
	v.hostInfo.SetText(text)
	v.interval.SetText(fmt.Sprintf("%ds", int(v.a.interval.Seconds())))
}

// overSpeedShow is how long an over-speed warning stays in the status line.
const overSpeedShow = time.Minute

// overSpeedEvent is a measured rate above the interface speed. On a physical
// port this means the router refreshes its counters less often than the
// poll interval, so single samples are too high (and their neighbors too
// low); a longer poll interval helps.
type overSpeedEvent struct {
	name  string
	rate  float64 // bit/s
	speed uint64  // bit/s
	at    time.Time
}

// findOverSpeed returns the interface whose latest rate exceeds its speed
// by the largest factor.
func findOverSpeed(all []Iface) (overSpeedEvent, bool) {
	var ev overSpeedEvent
	worst := 1.0
	for i := range all {
		ifc := &all[i]
		s, ok := ifc.Latest()
		if !ok || ifc.Speed == 0 {
			continue
		}
		if r := max(s.InBps, s.OutBps); r/float64(ifc.Speed) > worst {
			worst = r / float64(ifc.Speed)
			ev = overSpeedEvent{name: ifc.Name, rate: r, speed: ifc.Speed}
		}
	}
	return ev, worst > 1
}

func (v *MainView) refresh(r PollResult) {
	v.renderTable()
	help := "  [gray]Enter: graph  /: search  s: sort  a/o/i/p: filters  F1: help"
	if ev := v.a.overSpeed; ev != nil && time.Since(ev.at) < overSpeedShow {
		// replaces the key hints (errors below still take precedence)
		help = fmt.Sprintf("  [yellow]⚠ %s %s: %s > %s – the router updates its counters less often than the poll interval; increase the poll interval (e.g. 10 s)",
			ev.at.Format("15:04:05"), tview.Escape(ev.name), formatRate(ev.rate, "b/s"), formatRate(float64(ev.speed), "b/s"))
	}
	dur := fmt.Sprintf("%.1f s", r.Duration.Seconds())
	switch {
	case r.Err != nil:
		v.status.SetText(fmt.Sprintf("[red]%s error: %s%s",
			r.T.Format("15:04:05"), tview.Escape(r.Err.Error()), help))
	case r.DiscErr != nil:
		v.status.SetText(fmt.Sprintf("[yellow]%s (%s) – interface discovery error: %s; measuring goes on with the known list%s",
			r.T.Format("15:04:05"), dur, tview.Escape(r.DiscErr.Error()), help))
	case r.Duration > v.a.interval:
		v.status.SetText(fmt.Sprintf("[yellow]Last poll: %s, %s – slower than the poll interval!%s",
			r.T.Format("15:04:05"), dur, help))
	default:
		v.status.SetText(fmt.Sprintf("[green]Last poll: %s (%s)%s", r.T.Format("15:04:05"), dur, help))
	}
}

// visible reports whether ifc passes the admin/oper down filters and the
// search text.
func (v *MainView) visible(ifc *Iface) bool {
	if !(v.showAdminDown || ifc.AdminUp()) || !(v.showOperDown || ifc.OperUp()) {
		return false
	}
	if v.query == "" {
		return true
	}
	q := strings.ToLower(v.query)
	for _, s := range []string{ifc.Name, ifc.Alias, ifc.Descr} {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// toggle flips a display filter ('a', 'o' or 'i') and redraws the table.
func (v *MainView) toggle(key rune) {
	switch key {
	case 'a':
		v.showAdminDown = !v.showAdminDown
	case 'o':
		v.showOperDown = !v.showOperDown
	case 'i':
		v.showIdx = !v.showIdx
	case 'p':
		v.showPackets = !v.showPackets
		if !v.sortAvailable(sortModes[v.sortIdx]) {
			v.sortIdx = 0 // sorted by a column that is now hidden
		}
	}
	v.renderTable()
}

// cycleSort switches to the next (step 1) or previous (-1) ordering.
// Orderings by hidden columns are skipped.
func (v *MainView) cycleSort(step int) {
	for i := 0; i < len(sortModes); i++ {
		v.sortIdx = (v.sortIdx + step + len(sortModes)) % len(sortModes)
		if v.sortAvailable(sortModes[v.sortIdx]) {
			break
		}
	}
	v.renderTable()
}

// sortAvailable reports whether the column of an ordering is shown.
func (v *MainView) sortAvailable(m sortMode) bool {
	return v.showPackets || !strings.HasSuffix(m.column, "p/s")
}

// startSearch shows the search field in the bottom line.
func (v *MainView) startSearch() {
	v.bottom.SwitchToPage("search")
	v.a.app.SetFocus(v.search)
}

// clearSearch removes an active search; it reports whether there was one.
func (v *MainView) clearSearch() bool {
	if v.query == "" {
		return false
	}
	v.search.SetText("")
	return true
}

func shownText(b bool) string {
	if b {
		return "shown"
	}
	return "hidden"
}

func upDownCell(v int) *tview.TableCell {
	color := tcell.ColorRed
	if v == 1 {
		color = tcell.ColorGreen
	}
	return tview.NewTableCell(statusLetter(v)).SetTextColor(color)
}

// pctCell shows a rate as a percentage of the interface speed, colored when
// the link is busy.
func pctCell(bps float64, speed uint64, ok bool) *tview.TableCell {
	if !ok || speed == 0 {
		return tview.NewTableCell(fmt.Sprintf("%*s", pctWidth, "-")).SetAlign(tview.AlignRight)
	}
	pct := bps / float64(speed) * 100
	cell := tview.NewTableCell(fmt.Sprintf("%*.1f", pctWidth, min(pct, 999.9))).SetAlign(tview.AlignRight)
	switch {
	case pct >= 90:
		cell.SetTextColor(tcell.ColorRed)
	case pct >= 70:
		cell.SetTextColor(tcell.ColorYellow)
	}
	return cell
}

// tableTitle builds the table title fitted to the screen width: the stale
// warning and the interface count always stay, the filter states, then the
// sort and search parts are left out first when it does not fit.
func (v *MainView) tableTitle(shown, total int, mode sortMode, stale bool) string {
	type part struct {
		text string
		drop int // drop order when too wide (0 = never)
	}
	parts := []part{{fmt.Sprintf("Interfaces: %d/%d", shown, total), 0}}
	if stale {
		parts = append([]part{{v.a.staleText(), 0}}, parts...)
	}
	parts = append(parts,
		part{"admin down: " + shownText(v.showAdminDown), 1},
		part{"oper down: " + shownText(v.showOperDown), 2})
	if v.sortIdx != 0 {
		parts = append(parts, part{"sort: " + mode.name, 3})
	}
	if v.query != "" {
		parts = append(parts, part{fmt.Sprintf("search: %q", v.query), 4})
	}
	join := func(ps []part) string {
		texts := make([]string, len(ps))
		for i, p := range ps {
			texts[i] = p.text
		}
		return " " + strings.Join(texts, "  │  ") + " "
	}
	room := v.a.screenW - 4 // border corners and a margin
	for d := 1; d <= 4 && v.a.screenW > 0 && len([]rune(join(parts))) > room; d++ {
		for i := range parts {
			if parts[i].drop == d {
				parts = append(parts[:i], parts[i+1:]...)
				break
			}
		}
	}
	title := tview.Escape(join(parts))
	if stale { // the warning (always first) in red
		w := tview.Escape(v.a.staleText())
		title = strings.Replace(title, w, "[red::b]"+w+"[-::-]", 1)
	}
	return title
}

func (v *MainView) renderTable() {
	type column struct {
		title string
		right bool
		width int // fixed content width, 0 = computed (Name, Description)
	}
	columns := []column{{"Name", false, 0}, {"Description", false, 0}, {"A", false, 1}, {"O", false, 1},
		{"In b/s", true, rateWidth}, {"Out b/s", true, rateWidth}, {"In%", true, pctWidth}, {"Out%", true, pctWidth}}
	if v.showPackets {
		columns = append(columns, column{"In p/s", true, rateWidth}, column{"Out p/s", true, rateWidth})
	}
	if v.showIdx {
		columns = append([]column{{"Idx", true, 0}}, columns...)
	}
	mode := sortModes[v.sortIdx]
	stale := v.a.stale()

	all := v.a.store.Snapshot()
	sort.SliceStable(all, func(i, j int) bool { return mode.less(&all[i], &all[j]) })
	var shown []*Iface
	for i := range all {
		if v.visible(&all[i]) {
			shown = append(shown, &all[i])
		}
	}

	// The description gets what the other columns leave of the screen
	// width (between minDescrLen and maxDescrLen).
	descLen := maxDescrLen
	if v.a.screenW > 0 {
		used := 2 + len(columns) - 1 // table border, column separators
		for _, col := range columns {
			switch col.title {
			case "Description":
			case "Name":
				w := len("Name") + 1 // room for the sort marker
				for _, ifc := range shown {
					w = max(w, len([]rune(truncate(ifc.Name, maxNameLen))))
				}
				used += w
			case "Idx":
				w := len("Idx")
				for _, ifc := range shown {
					w = max(w, len(fmt.Sprint(ifc.Index)))
				}
				used += w
			default:
				used += max(col.width, len(col.title)+1)
			}
		}
		descLen = min(max(v.a.screenW-used, minDescrLen), maxDescrLen)
	}

	// Rebuilding the table must not move the view: restore the offset below.
	rowOffset, _ := v.table.GetOffset()
	v.table.Clear()
	for c, col := range columns {
		title := col.title
		if title == mode.column {
			if mode.column == "Name" || mode.column == "Description" {
				title += "▲"
			} else {
				title += "▼"
			}
		}
		cell := tview.NewTableCell(title).
			SetTextColor(tcell.ColorYellow).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false)
		if col.right {
			cell.SetAlign(tview.AlignRight)
		}
		v.table.SetCell(0, c, cell)
	}

	v.rowIndex = v.rowIndex[:0]
	selRow := -1
	for _, ifc := range shown {
		idx := ifc.Index
		v.rowIndex = append(v.rowIndex, idx)
		row := len(v.rowIndex)
		if idx == v.selected {
			selRow = row
		}

		s, ok := ifc.Latest()
		// Stale data stays visible but greyed out.
		gray := func(c *tview.TableCell) *tview.TableCell {
			if stale {
				c.SetTextColor(tcell.ColorGray)
			}
			return c
		}
		rate := func(val float64, unit string) *tview.TableCell {
			text := fmt.Sprintf("%*s", rateWidth, "-")
			if ok {
				text = formatRateFixed(val, unit)
			}
			return gray(tview.NewTableCell(text).SetAlign(tview.AlignRight))
		}
		var cells []*tview.TableCell
		if v.showIdx {
			cells = append(cells, tview.NewTableCell(fmt.Sprint(idx)).SetAlign(tview.AlignRight))
		}
		cells = append(cells,
			tview.NewTableCell(tview.Escape(truncate(ifc.Name, maxNameLen))),
			tview.NewTableCell(tview.Escape(truncate(ifc.Description(), descLen))).SetExpansion(1),
			upDownCell(ifc.Admin),
			upDownCell(ifc.Oper),
			rate(s.InBps, "b/s"), rate(s.OutBps, "b/s"),
			gray(pctCell(s.InBps, ifc.Speed, ok)), gray(pctCell(s.OutBps, ifc.Speed, ok)),
		)
		if v.showPackets {
			cells = append(cells, rate(s.InPps, "p/s"), rate(s.OutPps, "p/s"))
		}
		for c, cell := range cells {
			cell.SetClickedFunc(func() bool {
				v.a.openGraph(idx)
				return false
			})
			v.table.SetCell(row, c, cell)
		}
	}

	v.table.SetTitle(v.tableTitle(len(v.rowIndex), len(all), mode, stale))

	// SetOffset also clears tview's "follow the last row" mode, which it turns
	// on while the table is short (e.g. empty at startup) and which would
	// otherwise scroll a long list to its end once rows arrive.
	v.table.SetOffset(rowOffset, 0)

	if selRow < 0 && len(v.rowIndex) > 0 {
		selRow = 1
	}
	// Select() makes tview scroll back to the selected row, so only call it
	// when the row moved; otherwise the user's scroll position is kept.
	if cur, _ := v.table.GetSelection(); selRow > 0 && selRow != cur {
		v.table.Select(selRow, 0)
	}
}
