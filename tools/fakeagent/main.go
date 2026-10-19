// fakeagent runs the internal/fakeagent SNMPv2c / SNMPv3 test agent for
// manually testing tsnmp without a real device (interfaces: see
// fakeagent.DefaultIfaces).
//
// Usage: go run ./tools/fakeagent [-listen 127.0.0.1:16161] [-community public] [-extra 50] [-delay 0] [-refresh 0] [-hot]
// [-user tsnmp -auth SHA -authpass authpass123 -priv AES -privpass privpass123 -context ""]
// then connect tsnmp to 127.0.0.1:16161 (v2c with the community, or v3 with
// the user; -user "" disables SNMPv3).
//
// While running, type a command and Enter on stdin:
//
//	reset N   zero the counters of ifIndex N ("clear counters")
//	reboot    restart sysUpTime and zero all counters
//	down / up stop / resume answering (connection lost)
//	nodisc    ignore discovery requests (ifAlias), polling still works
//	disc      answer discovery requests again
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"tsnmp/internal/fakeagent"
	"tsnmp/internal/usm"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:16161", "UDP listen address")
	community := flag.String("community", "public", "accepted community string")
	extra := flag.Int("extra", 50, "number of additional ge-0/0/N interfaces")
	delay := flag.Duration("delay", 0, "wait before every response (e.g. 50ms, simulates a slow link)")
	refresh := flag.Duration("refresh", 0, "counters change only this often (e.g. 2s, like a router's counter cache)")
	hot := flag.Bool("hot", false, "add xe-1/0/0: 10 Gb/s port with a constant 85% load")
	user := flag.String("user", "tsnmp", "SNMPv3 user (\"\": SNMPv3 disabled)")
	authName := flag.String("auth", "SHA", "SNMPv3 authentication protocol: "+strings.Join(usm.AuthNames, ", "))
	authPass := flag.String("authpass", "authpass123", "SNMPv3 authentication password")
	privName := flag.String("priv", "AES", "SNMPv3 privacy protocol: "+strings.Join(usm.PrivNames, ", "))
	privPass := flag.String("privpass", "privpass123", "SNMPv3 privacy password")
	context := flag.String("context", "", "the only accepted SNMPv3 context name (\"\": default context)")
	flag.Parse()

	opts := fakeagent.Options{Delay: *delay, CounterRefresh: *refresh}
	v3desc := "SNMPv3 disabled"
	if *user != "" {
		auth, ok1 := usm.Auth(*authName)
		priv, ok2 := usm.Priv(*privName)
		if !ok1 || !ok2 {
			log.Fatalf("unknown protocol: -auth %q / -priv %q", *authName, *privName)
		}
		opts.V3 = &fakeagent.V3User{Name: *user, Auth: auth, AuthPass: *authPass, Priv: priv, PrivPass: *privPass, Context: *context}
		v3desc = fmt.Sprintf("SNMPv3 user %q auth %s %q priv %s %q context %q",
			*user, usm.AuthName(auth), *authPass, usm.PrivName(priv), *privPass, *context)
	}

	ifs := fakeagent.DefaultIfaces(*extra)
	if *hot {
		ifs[5] = fakeagent.Iface{Name: "xe-1/0/0", Descr: "10G port", Alias: "busy uplink", Admin: 1, Oper: 1,
			HC: true, Bps: 8.5e9, Flat: true, SpeedMbps: 10000}
	}
	a, err := fakeagent.Start(*listen, *community, ifs, opts)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fake SNMP agent on %s (v2c community %q, %s, %d interfaces)", a.Addr(), *community, v3desc, len(ifs))

	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var idx int
		switch line := strings.TrimSpace(in.Text()); {
		case line == "reboot":
			a.Reboot()
			log.Print("rebooted")
		case line == "down" || line == "up":
			a.SetDown(line == "down")
			log.Print(line)
		case line == "nodisc":
			a.SetDropPrefix(".1.3.6.1.2.1.31.1.1.1.18") // ifAlias: only discovery asks for it
			log.Print("discovery requests ignored")
		case line == "disc":
			a.SetDropPrefix("")
			log.Print("discovery requests answered")
		case strings.HasPrefix(line, "reset"):
			if _, err := fmt.Sscanf(line, "reset %d", &idx); err == nil {
				a.ResetCounters(idx)
				log.Printf("counters of ifIndex %d reset", idx)
			}
		}
	}
	select {} // stdin closed (e.g. started in background): keep serving
}
