// tsnmp: SNMPv2c / SNMPv3 interface traffic monitor with a text user interface.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"tsnmp/internal/usm"
)

// version is set by release builds: -ldflags "-X main.version=1.2.3".
var version = "dev"

type App struct {
	app   *tview.Application
	pages *tview.Pages
	store *Store

	host     string
	sysName  string
	snmp     Config // connection settings (for the header)
	client   *Client
	poller   *Poller
	interval time.Duration

	main  *MainView
	graph *GraphView

	// poll health, for marking stale data (UI goroutine only)
	lastOK  time.Time     // end of the last successful counter poll
	lastDur time.Duration // duration of the last poll
	pollErr error         // error of the last poll (nil = data is fresh)
	screenW int           // screen width at the last draw

	overSpeed *overSpeedEvent // last rate above an interface's speed

	helpReturnFocus     tview.Primitive // focus to restore when help closes
	licensesReturnFocus tview.Primitive // focus to restore when the licenses close
	confirmReturnFocus  tview.Primitive // focus to restore when a confirmation is cancelled
	confirmOK           func()          // action of the open confirmation
}

func main() {
	snmpVersion := flag.String("v", "2c", "SNMP version: 2c or 3")
	community := flag.String("c", "public", "SNMP community (v2c)")
	user := flag.String("u", "", "SNMPv3 user name")
	authProto := flag.String("a", "", "SNMPv3 authentication protocol: "+strings.Join(usm.AuthNames, ", ")+
		"\n(default: SHA if -A is given, else none)")
	authPass := flag.String("A", "", "SNMPv3 authentication password")
	privProto := flag.String("x", "", "SNMPv3 privacy protocol: "+strings.Join(usm.PrivNames, ", ")+
		"\n(default: AES if -X is given, else none)")
	privPass := flag.String("X", "", "SNMPv3 privacy password")
	context := flag.String("n", "", "SNMPv3 context name (default: the default context)")
	licenses := flag.Bool("licenses", false, "print the license of tsnmp and of the third-party software it includes, then exit")
	showVersion := flag.Bool("version", false, "print the version of tsnmp, then exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: %s [-v 2c] [-c community] [address[:port]]\n"+
				"       %s -v 3 -u user [-a proto -A password [-x proto -X password]] [-n context] [address[:port]]\n\n"+
				"The address is an IP address or hostname. If given, the program connects\n"+
				"right away, otherwise it asks for it (and the SNMP settings) on the login\n"+
				"screen. The security level follows from the protocols given: none,\n"+
				"authentication only, or authentication and privacy (encryption).\n\n",
			os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("tsnmp", version)
		return
	}
	if *licenses {
		fmt.Print(licensesText())
		return
	}
	st, err := loginFromFlags(*snmpVersion, *authProto, *privProto, loginState{community: *community,
		user: *user, authPass: *authPass, privPass: *privPass, context: *context})
	if err == nil && flag.NArg() > 1 {
		err = fmt.Errorf("too many arguments")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}
	st.target = flag.Arg(0)

	a := &App{
		app:      tview.NewApplication(),
		pages:    tview.NewPages(),
		store:    NewStore(),
		interval: defaultInterval,
	}
	a.pages.AddPage("login", a.loginView(st), true, true)
	a.app.SetInputCapture(a.globalKeys)
	a.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		// The description column follows the width: re-layout on resize.
		if w, _ := screen.Size(); w != a.screenW {
			a.screenW = w
			if a.main != nil {
				a.main.updateHeader()
				a.main.renderTable()
			}
		}
		return false
	})
	a.app.EnableMouse(true)
	if err := a.app.SetRoot(a.pages, true).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if a.client != nil {
		a.client.Close()
	}
}

// loginFromFlags completes the login form values from the command line:
// st has the plain text options, the rest are parsed here.
func loginFromFlags(version, authProto, privProto string, st loginState) (*loginState, error) {
	switch strings.TrimPrefix(strings.ToLower(version), "v") {
	case "2c", "2":
	case "3":
		st.v3 = true
	default:
		return nil, fmt.Errorf("invalid SNMP version: %q (2c or 3)", version)
	}
	if authProto == "" && st.authPass != "" {
		authProto = "SHA"
	}
	if privProto == "" && st.privPass != "" {
		privProto = "AES"
	}
	var ok bool
	if st.auth, ok = usm.Auth(authProto); !ok {
		return nil, fmt.Errorf("invalid authentication protocol: %q", authProto)
	}
	if st.priv, ok = usm.Priv(privProto); !ok {
		return nil, fmt.Errorf("invalid privacy protocol: %q", privProto)
	}
	return &st, nil
}

// start switches to the main screen and launches the poller.
func (a *App) start(host, name string, cfg Config, client *Client) {
	a.host, a.sysName, a.snmp, a.client = host, name, cfg, client
	a.main = newMainView(a)
	a.graph = newGraphView(a)
	a.pages.AddPage("main", a.main.root, true, false)
	a.pages.AddPage("graph", a.graph.root, true, false)
	a.pages.SwitchToPage("main")
	a.pages.RemovePage("login")
	a.app.SetFocus(a.main.table)

	a.poller = NewPoller(client, a.store, func(r PollResult) {
		a.app.QueueUpdateDraw(func() { a.onPoll(r) })
	})
	go a.poller.Run(a.interval)
	// Re-check staleness between polls too: a lost connection can keep a
	// poll busy with timeouts for a long time without any result.
	go func() {
		for range time.Tick(time.Second) {
			a.app.QueueUpdateDraw(func() {
				if !a.lastOK.IsZero() && a.stale() {
					a.main.renderTable()
				}
			})
		}
	}()
}

func (a *App) onPoll(r PollResult) {
	a.pollErr, a.lastDur = r.Err, r.Duration
	if r.Err == nil {
		a.lastOK = r.T.Add(r.Duration)
		if ev, ok := findOverSpeed(a.store.Snapshot()); ok {
			ev.at = r.T
			a.overSpeed = &ev
		}
	}
	a.main.refresh(r)
	a.graph.refresh()
}

// stale reports whether the shown data is out of date: the last poll failed
// or no successful poll finished for much longer than expected.
func (a *App) stale() bool {
	if a.lastOK.IsZero() {
		return a.pollErr != nil
	}
	return a.pollErr != nil || time.Since(a.lastOK) > 2*a.interval+2*a.lastDur+time.Second
}

// staleText is the warning shown on stale data.
func (a *App) staleText() string {
	if a.lastOK.IsZero() {
		return "⚠ CONNECTION ERROR – no data yet"
	}
	return "⚠ CONNECTION ERROR – last data: " + a.lastOK.Format("15:04:05")
}

func (a *App) changeInterval(up bool) {
	step := time.Second
	if a.interval > 10*time.Second || (up && a.interval == 10*time.Second) {
		step = 5 * time.Second
	}
	if up {
		a.interval += step
	} else {
		a.interval -= step
	}
	a.interval = min(max(a.interval, minInterval), maxInterval)
	a.main.updateHeader()
	a.poller.SetInterval(a.interval)
}

func (a *App) openGraph(ifIndex int) {
	a.graph.show(ifIndex)
	a.pages.SwitchToPage("graph")
	a.app.SetFocus(a.graph.root)
}

func (a *App) closeGraph() {
	a.pages.SwitchToPage("main")
	a.app.SetFocus(a.main.table)
}

const helpText = `[yellow::b]Main screen[-::-]
  ↑ / ↓, PgUp / PgDn, Home / End  move between interfaces
  Enter, mouse click              traffic graph of the interface
  + / -                           increase / decrease the poll interval
  a / A                           show / hide admin down interfaces
  o / O                           show / hide oper down interfaces
  i / I                           show / hide the Idx column
  p / P                           show / hide the p/s columns
  /                               search in name / description
                                  (Enter: keep, Esc: clear)
  s / S                           sort: ifIndex, name, descr., b/s, %, p/s
  r / R                           restart the measurement: delete all data
                                  (with confirmation)
  F1                              help
  Esc, q                          quit (with confirmation)

[yellow::b]Graph[-::-]  (min/max: over the plotted period)
  ← / →                           scroll back / forward by one point
  Shift+← / Shift+→, PgUp / PgDn  scroll by half / full screen
  Home / End                      live view / oldest measurement
  z / Z                           cycle zoom: 1× → 5× → 30× → 120× → 1×
  + / -                           increase / decrease the poll interval
  r / R                           restart the measurement (with confirmation)
  Esc, right click                back to the list

[yellow::b]Columns[-::-]
  A / O        admin / oper status: [green]U[-] = up, [red]D[-] = down
  In% / Out%   utilization relative to the interface speed
               ([yellow]yellow[-] above 70%, [red]red[-] above 90%)

[gray]Close: Esc or F1, scroll: ↑ / ↓, l: licenses[-]

Copyright (c) 2026 Zoltán Kiss, MIT License`

func (a *App) showHelp() {
	text := tview.NewTextView().SetDynamicColors(true).SetText(helpText)
	text.SetBorder(true).SetTitle(" Help ")
	box := &centered{Box: tview.NewBox(), child: text, w: 79, h: strings.Count(helpText, "\n") + 3}
	a.helpReturnFocus = a.app.GetFocus()
	a.pages.AddPage("help", box, true, true)
	a.app.SetFocus(text)
}

// centered draws child in the middle of the screen at size w×h, shrunk to
// fit small terminals (the child then scrolls).
type centered struct {
	*tview.Box
	child tview.Primitive
	w, h  int
}

// Focus, HasFocus and InputHandler pass through to the child: tview routes
// key events from the root down via HasFocus.
func (c *centered) Focus(delegate func(p tview.Primitive)) { delegate(c.child) }
func (c *centered) HasFocus() bool                         { return c.child.HasFocus() }
func (c *centered) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return c.child.InputHandler()
}

// MouseHandler passes mouse events (e.g. the wheel) to the child and swallows
// the rest, so they don't reach the pages underneath.
func (c *centered) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return c.WrapMouseHandler(func(action tview.MouseAction, ev *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		if consumed, capture := c.child.MouseHandler()(action, ev, setFocus); consumed {
			return true, capture
		}
		return true, nil
	})
}

func (c *centered) Draw(screen tcell.Screen) {
	sw, sh := screen.Size()
	w, h := min(c.w, sw), min(c.h, sh)
	c.child.SetRect((sw-w)/2, (sh-h)/2, w, h)
	c.child.Draw(screen)
}

// confirm shows a yes/no dialog; "y" / "n" also answer it (see globalKeys).
// The cancel button is focused, so Enter alone does nothing harmful.
func (a *App) confirm(text, okLabel string, onOK func()) {
	m := tview.NewModal().
		SetText(text + "\n\n(y: yes, n / Esc: no)").
		AddButtons([]string{okLabel, "Cancel"}).
		SetDoneFunc(func(i int, _ string) {
			a.closeConfirm()
			if i == 0 {
				onOK()
			}
		})
	m.SetFocus(1)
	a.confirmOK = onOK
	a.confirmReturnFocus = a.app.GetFocus()
	a.pages.AddPage("confirm", m, true, true)
	a.app.SetFocus(m)
}

func (a *App) closeConfirm() {
	a.pages.RemovePage("confirm")
	if a.confirmReturnFocus != nil {
		a.app.SetFocus(a.confirmReturnFocus)
	}
}

// confirmQuit asks before quitting: all measurements live only in memory.
func (a *App) confirmQuit() {
	a.confirm("Quit?\nAll measured data will be lost.", "Quit", a.app.Stop)
}

// confirmReset asks before restarting the measurement.
func (a *App) confirmReset() {
	a.confirm("Restart the measurement?\nAll measured data and counters will be deleted.", "OK", a.resetData)
}

// resetData deletes all measurements; the interface list is kept. A poll
// already running only stores its counters as the new starting point.
func (a *App) resetData() {
	a.store.Reset()
	a.overSpeed = nil
	a.main.renderTable()
	a.graph.show(a.graph.ifIndex)
}

func (a *App) closeHelp() {
	a.pages.RemovePage("help")
	if a.helpReturnFocus != nil {
		a.app.SetFocus(a.helpReturnFocus)
	}
}

func (a *App) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	page, _ := a.pages.GetFrontPage()
	if page == "licenses" {
		switch ev.Key() {
		case tcell.KeyEscape:
			a.closeLicenses()
		case tcell.KeyF1:
			a.closeLicenses()
			a.closeHelp()
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
			return ev // scroll the licenses
		}
		return nil
	}
	if page == "help" {
		switch {
		case ev.Key() == tcell.KeyEscape, ev.Key() == tcell.KeyF1:
			a.closeHelp()
		case ev.Rune() == 'l' || ev.Rune() == 'L':
			a.showLicenses()
		case ev.Key() == tcell.KeyUp, ev.Key() == tcell.KeyDown, ev.Key() == tcell.KeyPgUp,
			ev.Key() == tcell.KeyPgDn, ev.Key() == tcell.KeyHome, ev.Key() == tcell.KeyEnd:
			return ev // scroll the help text
		}
		return nil
	}
	if page == "confirm" {
		switch {
		case ev.Rune() == 'y' || ev.Rune() == 'Y':
			a.closeConfirm()
			a.confirmOK()
			return nil
		case ev.Rune() == 'n' || ev.Rune() == 'N':
			a.closeConfirm()
			return nil
		}
		return ev // Esc, Enter, Tab, arrows: handled by the dialog
	}
	if ev.Key() == tcell.KeyF1 {
		a.showHelp()
		return nil
	}
	if a.main != nil && a.app.GetFocus() == a.main.search {
		return ev // typing a search: every key goes to the input field
	}
	switch page {
	case "login":
		if _, list := a.app.GetFocus().(*tview.List); list {
			return ev // an open drop-down list: Esc closes it
		}
		if ev.Key() == tcell.KeyEscape {
			a.app.Stop()
			return nil
		}
	case "main", "graph":
		switch {
		case ev.Key() == tcell.KeyEscape && page == "graph":
			a.closeGraph()
			return nil
		case page == "graph" && a.graph.handleKey(ev):
			return nil
		case ev.Key() == tcell.KeyEscape && a.main.clearSearch():
			return nil // Esc first clears an active search
		case ev.Key() == tcell.KeyEscape, ev.Rune() == 'q', ev.Rune() == 'Q':
			a.confirmQuit()
			return nil
		case ev.Rune() == 'r', ev.Rune() == 'R':
			a.confirmReset()
			return nil
		case ev.Rune() == '+':
			a.changeInterval(true)
			return nil
		case ev.Rune() == '-':
			a.changeInterval(false)
			return nil
		case page == "main" && ev.Key() == tcell.KeyRune && strings.ContainsRune("aAoOiIpP", ev.Rune()):
			a.main.toggle(unicode.ToLower(ev.Rune()))
			return nil
		case page == "main" && ev.Rune() == '/':
			a.main.startSearch()
			return nil
		case page == "main" && ev.Rune() == 's':
			a.main.cycleSort(1)
			return nil
		case page == "main" && ev.Rune() == 'S':
			a.main.cycleSort(-1)
			return nil
		}
	}
	return ev
}
