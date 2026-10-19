package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/gosnmp/gosnmp"
	"github.com/rivo/tview"

	"tsnmp/internal/usm"
)

// loginState holds the login form values; it survives rebuilding the form
// when the SNMP version changes.
type loginState struct {
	v3        bool
	target    string
	community string
	user      string
	auth      gosnmp.SnmpV3AuthProtocol
	authPass  string
	priv      gosnmp.SnmpV3PrivProtocol
	privPass  string
	context   string
}

// config validates the form values. host is the target as typed.
func (st *loginState) config() (cfg Config, host string, err error) {
	host = strings.TrimSpace(st.target)
	ip, port, err := parseTarget(host)
	if err != nil {
		return cfg, host, err
	}
	cfg = Config{Host: ip, Port: port, Version: gosnmp.Version2c, Community: st.community}
	if cfg.Community == "" {
		cfg.Community = "public"
	}
	if st.v3 {
		cfg.Version = gosnmp.Version3
		cfg.User = strings.TrimSpace(st.user)
		cfg.Auth, cfg.AuthPass = st.auth, st.authPass
		cfg.Priv, cfg.PrivPass = st.priv, st.privPass
		cfg.Context = strings.TrimSpace(st.context)
	}
	return cfg, host, cfg.Validate()
}

const loginFieldWidth = 30

// loginView asks for the target and the SNMP settings (v2c: community, v3:
// user, authentication, privacy and context name). With a target given on the command
// line it connects right away (the form stays for retrying).
func (a *App) loginView(st *loginState) tview.Primitive {
	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" tsnmp – SNMP interface monitor ")
	status := tview.NewTextView().SetDynamicColors(true)
	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(form, 0, 0, true).
		AddItem(status, 2, 0, false)
	column := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(box, 0, 0, true).
		AddItem(nil, 0, 1, false)

	connecting := false
	doConnect := func() {
		if connecting {
			return
		}
		cfg, host, err := st.config()
		if err != nil {
			status.SetText("[red]" + tview.Escape(err.Error()))
			return
		}
		connecting = true
		status.SetText("[yellow]Connecting to " + tview.Escape(host) + " (SNMP " + tview.Escape(cfg.Describe()) + ") ...")
		go func() {
			client, name, err := connect(cfg)
			a.app.QueueUpdateDraw(func() {
				connecting = false
				if err != nil {
					status.SetText("[red]Error: " + tview.Escape(err.Error()))
					return
				}
				a.start(host, name, cfg, client)
			})
		}()
	}

	input := func(label, text string, mask bool, set func(string)) {
		f := tview.NewInputField().SetLabel(label).SetText(text).
			SetFieldWidth(loginFieldWidth).SetChangedFunc(set)
		if mask {
			f.SetMaskCharacter('*')
		}
		// Enter in any field connects right away (no need to Tab to the button).
		f.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				doConnect()
			}
		})
		form.AddFormItem(f)
	}
	dropDown := func(label string, options []string, current int, set func(int)) {
		d := tview.NewDropDown().SetLabel(label).SetOptions(options, nil).SetCurrentOption(current)
		d.SetSelectedFunc(func(_ string, i int) { set(i) }) // after SetCurrentOption: not called for it
		form.AddFormItem(d)
	}

	var build func(focus int)
	build = func(focus int) {
		form.Clear(false)
		version := 0
		if st.v3 {
			version = 1
		}
		dropDown("SNMP version", []string{"v2c", "v3"}, version, func(i int) {
			if (i == 1) != st.v3 {
				st.v3 = i == 1
				// Rebuild after the drop-down finished handling the event
				// (QueueUpdateDraw waits for the event loop: not from here).
				go a.app.QueueUpdateDraw(func() {
					build(0)
					a.app.SetFocus(form)
				})
			}
		})
		input("IP address / name", st.target, false, func(s string) { st.target = s })
		if !st.v3 {
			input("Community", st.community, false, func(s string) { st.community = s })
		} else {
			input("User", st.user, false, func(s string) { st.user = s })
			dropDown("Auth protocol", usm.AuthNames, int(st.auth)-1, func(i int) { st.auth = gosnmp.SnmpV3AuthProtocol(i + 1) })
			input("Auth password", st.authPass, true, func(s string) { st.authPass = s })
			dropDown("Privacy protocol", usm.PrivNames, int(st.priv)-1, func(i int) { st.priv = gosnmp.SnmpV3PrivProtocol(i + 1) })
			input("Privacy password", st.privPass, true, func(s string) { st.privPass = s })
			input("Context name", st.context, false, func(s string) { st.context = s })
		}
		// border + padding (4), items with a blank line between them, then
		// a blank line and the buttons
		h := 4 + 2*form.GetFormItemCount() + 1
		box.ResizeItem(form, h, 0)
		column.ResizeItem(box, h+2, 0)
		form.SetFocus(focus)
	}
	st.auth, st.priv = max(st.auth, gosnmp.NoAuth), max(st.priv, gosnmp.NoPriv)
	build(1) // start in the address field
	form.AddButton("Connect", doConnect)
	form.AddButton("Quit", a.app.Stop)
	if st.target != "" {
		doConnect()
	}

	// center the form on screen
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(column, 2+2+len("IP address / name")+1+loginFieldWidth+1, 0, true).
		AddItem(nil, 0, 1, false)
}

// parseTarget accepts an IP address or hostname, optionally with ":port"
// ("[IPv6]:port" for IPv6); the default port is 161.
func parseTarget(s string) (string, uint16, error) {
	if net.ParseIP(s) != nil {
		return s, 161, nil
	}
	// A bracketed IPv6 address without a port, e.g. "[::1]".
	if ip, ok := strings.CutPrefix(s, "["); ok {
		if ip, ok := strings.CutSuffix(ip, "]"); ok && net.ParseIP(ip) != nil {
			return ip, 161, nil
		}
	}
	host, port := s, uint64(161)
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, perr := strconv.ParseUint(p, 10, 16)
		if perr != nil || n == 0 {
			return "", 0, fmt.Errorf("invalid port: %q", p)
		}
		host, port = h, n
	}
	if net.ParseIP(host) == nil && !validHostname(host) {
		return "", 0, fmt.Errorf("invalid IP address or name: %q", s)
	}
	return host, uint16(port), nil
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}

func connect(cfg Config) (*Client, string, error) {
	c, err := NewClient(cfg)
	if err != nil {
		return nil, "", err
	}
	name, err := c.SysName()
	if err != nil {
		c.Close()
		return nil, "", err
	}
	return c, name, nil
}
