// Package fakeagent is a minimal in-process SNMPv2c / SNMPv3 (USM) agent for
// testing tsnmp without a real device. It serves sysName, sysUpTime and the IF-MIB columns
// tsnmp uses, with sinusoidal (60 s period) traffic on every interface.
// Counters can be reset and the agent "rebooted" to test discontinuities.
package fakeagent

import (
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	g "github.com/gosnmp/gosnmp"
)

// Iface describes one simulated interface.
type Iface struct {
	Name, Descr, Alias string
	Admin, Oper        int
	HC                 bool    // provide 64-bit ifXTable counters
	Bps                float64 // average inbound traffic in bit/s
	SpeedMbps          uint    // ifHighSpeed; ifSpeed is derived from it
	Flat               bool    // constant traffic instead of a 60 s sine wave
}

// DefaultIfaces is the standard test set:
//
//	1  lo     admin up, 64-bit counters, speed 0 (unknown)
//	2  eth0   admin up, long ifAlias (tests truncation), 1 Gb/s
//	3  eth1   admin DOWN
//	4  old0   admin up, oper down, 32-bit counters only (tests fallback), 100 Mb/s
//	10.. ge-0/0/N  extra additional ports (tests scrolling), 1 Gb/s
func DefaultIfaces(extra int) map[int]Iface {
	ifs := map[int]Iface{
		1: {"lo", "lo", "", 1, 1, true, 1e5, 0, false},
		2: {"eth0", "Intel eth0", "Uplink to core switch - very long description that should be truncated", 1, 1, true, 50e6, 1000, false},
		3: {"eth1", "Intel eth1", "admin down port", 2, 2, true, 0, 1000, false},
		4: {"old0", "Legacy 32bit", "32-bit only", 1, 2, false, 2e6, 100, false},
	}
	for n := 10; n < 10+extra; n++ {
		ifs[n] = Iface{fmt.Sprintf("ge-0/0/%d", n), "port", fmt.Sprintf("access port %d", n), 1, 1, true, float64(n) * 1e5, 1000, false}
	}
	return ifs
}

type value struct {
	typ g.Asn1BER
	get func() interface{}
}

// Options tune the agent's behavior.
type Options struct {
	// MaxResponseVarbinds truncates GetBulk responses like a real agent with
	// a limited message size (0 = no limit).
	MaxResponseVarbinds int
	// Delay is waited before every response (simulates a distant agent).
	Delay time.Duration
	// CounterRefresh makes the counters change only this often, like
	// routers that serve SNMP from a periodically refreshed cache (0 = live).
	CounterRefresh time.Duration
	// V3 enables SNMPv3 with one USM user (v2c with the community keeps
	// working).
	V3 *V3User
}

// Agent is a running fake agent.
type Agent struct {
	opts      Options
	conn      *net.UDPConn
	community string

	mu         sync.Mutex
	down       bool              // no answers at all (connection lost)
	dropPrefix string            // no answer to requests asking for this OID subtree
	boot       time.Time         // sysUpTime zero
	counterT0  map[int]time.Time // per interface: when its counters were last zero
	discont    map[int]uint32    // ifCounterDiscontinuityTime (sysUpTime ticks)
	boots      uint32            // snmpEngineBoots, incremented by Reboot

	v3 *v3Engine // nil: SNMPv3 disabled

	keys []string
	mib  map[string]value
}

// Start listens on addr (e.g. "127.0.0.1:0" for a random port).
func Start(addr, community string, ifs map[int]Iface, opts Options) (*Agent, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	a := &Agent{opts: opts, conn: conn, community: community, boot: now, boots: 1,
		counterT0: map[int]time.Time{}, discont: map[int]uint32{}}
	if opts.V3 != nil {
		if a.v3, err = newV3Engine(*opts.V3); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	for idx := range ifs {
		a.counterT0[idx] = now
	}
	a.mib = a.buildMIB(ifs)
	for k := range a.mib {
		a.keys = append(a.keys, k)
	}
	sort.Slice(a.keys, func(i, j int) bool { return oidLess(a.keys[i], a.keys[j]) })
	go a.serve()
	return a, nil
}

// Addr is the UDP address the agent listens on.
func (a *Agent) Addr() *net.UDPAddr { return a.conn.LocalAddr().(*net.UDPAddr) }

func (a *Agent) Close() { _ = a.conn.Close() }

// ResetCounters zeroes the counters of one interface ("clear counters") and
// updates its ifCounterDiscontinuityTime.
func (a *Agent) ResetCounters(idx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counterT0[idx] = time.Now()
	a.discont[idx] = a.uptimeLocked()
}

// SetDown makes the agent stop (true) or resume (false) answering.
func (a *Agent) SetDown(down bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.down = down
}

// SetDropPrefix makes the agent ignore requests that ask for an OID under
// prefix (e.g. ifAlias, so only discovery fails); "" turns it off.
func (a *Agent) SetDropPrefix(prefix string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropPrefix = prefix
}

func (a *Agent) ignored(req *g.SnmpPacket) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.down {
		return true
	}
	for _, v := range req.Variables {
		if a.dropPrefix != "" && strings.HasPrefix(v.Name+".", a.dropPrefix+".") {
			return true
		}
	}
	return false
}

// Reboot restarts sysUpTime and zeroes all counters.
func (a *Agent) Reboot() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.boot = now
	a.boots++
	for idx := range a.counterT0 {
		a.counterT0[idx] = now
		a.discont[idx] = 0
	}
}

func (a *Agent) uptimeLocked() uint32 {
	return uint32(time.Since(a.boot) / (10 * time.Millisecond))
}

func (a *Agent) buildMIB(ifs map[int]Iface) map[string]value {
	str := func(s string) value { return value{g.OctetString, func() interface{} { return []byte(s) }} }
	num := func(n int) value { return value{g.Integer, func() interface{} { return n }} }
	gauge := func(n uint) value { return value{g.Gauge32, func() interface{} { return n }} }

	mib := map[string]value{
		".1.3.6.1.2.1.1.5.0": str("fake-router"),
		".1.3.6.1.2.1.1.3.0": {g.TimeTicks, func() interface{} {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.uptimeLocked()
		}},
	}
	start := time.Now()
	for idx, f := range ifs {
		idx, f := idx, f
		x := fmt.Sprintf(".%d", idx)
		mib[".1.3.6.1.2.1.2.2.1.2"+x] = str(f.Descr)
		mib[".1.3.6.1.2.1.2.2.1.5"+x] = gauge(min(f.SpeedMbps*1_000_000, math.MaxUint32))
		mib[".1.3.6.1.2.1.2.2.1.7"+x] = num(f.Admin)
		mib[".1.3.6.1.2.1.2.2.1.8"+x] = num(f.Oper)
		mib[".1.3.6.1.2.1.31.1.1.1.1"+x] = str(f.Name)
		mib[".1.3.6.1.2.1.31.1.1.1.15"+x] = gauge(f.SpeedMbps)
		mib[".1.3.6.1.2.1.31.1.1.1.18"+x] = str(f.Alias)
		mib[".1.3.6.1.2.1.31.1.1.1.19"+x] = value{g.TimeTicks, func() interface{} {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.discont[idx]
		}}

		// Counter value = integral of a sinusoidal rate since the counters
		// were last zeroed, scaled per column.
		integral := func(t float64) float64 { return t - 30/math.Pi*math.Cos(t*math.Pi/30) }
		if f.Flat {
			integral = func(t float64) float64 { return t }
		}
		total := func(scale float64) float64 {
			a.mu.Lock()
			t0 := a.counterT0[idx].Sub(start).Seconds()
			a.mu.Unlock()
			el := time.Since(start)
			if r := a.opts.CounterRefresh; r > 0 {
				el = el.Truncate(r) // value of the last cache refresh
			}
			t := max(el.Seconds(), t0)
			return f.Bps * scale / 8 * (integral(t) - integral(t0))
		}
		c32 := func(scale float64) value {
			return value{g.Counter32, func() interface{} { return uint(uint32(uint64(total(scale)))) }}
		}
		c64 := func(scale float64) value {
			return value{g.Counter64, func() interface{} { return uint64(total(scale)) }}
		}

		t := ".1.3.6.1.2.1.2.2.1."
		mib[t+"10"+x] = c32(1)           // ifInOctets
		mib[t+"16"+x] = c32(0.3)         // ifOutOctets
		mib[t+"11"+x] = c32(1.0 / 800)   // ifInUcastPkts
		mib[t+"12"+x] = c32(0.01 / 800)  // ifInNUcastPkts
		mib[t+"17"+x] = c32(0.3 / 800)   // ifOutUcastPkts
		mib[t+"18"+x] = c32(0.003 / 800) // ifOutNUcastPkts
		if f.HC {
			xt := ".1.3.6.1.2.1.31.1.1.1."
			mib[xt+"6"+x] = c64(1)                      // ifHCInOctets
			mib[xt+"10"+x] = c64(0.3)                   // ifHCOutOctets
			for _, c := range []string{"7", "8", "9"} { // ifHCIn{Ucast,Multicast,Broadcast}Pkts
				mib[xt+c+x] = c64(1.0 / 2400)
			}
			for _, c := range []string{"11", "12", "13"} { // ifHCOut{Ucast,Multicast,Broadcast}Pkts
				mib[xt+c+x] = c64(0.3 / 2400)
			}
		}
	}
	return mib
}

func (a *Agent) get(name string) g.SnmpPDU {
	v, ok := a.mib[name]
	if !ok {
		return g.SnmpPDU{Name: name, Type: g.NoSuchObject}
	}
	return g.SnmpPDU{Name: name, Type: v.typ, Value: v.get()}
}

func (a *Agent) next(name string) g.SnmpPDU {
	i := sort.Search(len(a.keys), func(i int) bool { return oidLess(name, a.keys[i]) })
	if i == len(a.keys) {
		return g.SnmpPDU{Name: name, Type: g.EndOfMibView}
	}
	return a.get(a.keys[i])
}

func (a *Agent) serve() {
	dec := &g.GoSNMP{Logger: g.NewLogger(nil)}
	buf := make([]byte, 65535)
	for {
		n, from, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			continue
		}
		var b []byte
		if msgVersion(buf[:n]) == g.Version3 {
			if a.v3 == nil {
				continue
			}
			b = a.serveV3(append([]byte(nil), buf[:n]...))
		} else {
			b = a.serveV2c(dec, buf[:n])
		}
		if b == nil {
			continue
		}
		time.Sleep(a.opts.Delay)
		_, _ = a.conn.WriteToUDP(b, from)
	}
}

func (a *Agent) serveV2c(dec *g.GoSNMP, msg []byte) []byte {
	req, err := dec.SnmpDecodePacket(msg)
	if err != nil || req.Version != g.Version2c || req.Community != a.community || a.ignored(req) {
		return nil // wrong community: no answer, like a real agent
	}
	out := a.handle(req)
	if out == nil {
		return nil
	}
	resp := &g.SnmpPacket{Version: g.Version2c, Community: req.Community, PDUType: g.GetResponse,
		RequestID: req.RequestID, Variables: out}
	b, err := resp.MarshalMsg()
	if err != nil {
		return nil
	}
	return b
}

func (a *Agent) handle(req *g.SnmpPacket) []g.SnmpPDU {
	var out []g.SnmpPDU
	switch req.PDUType {
	case g.GetRequest:
		for _, v := range req.Variables {
			out = append(out, a.get(v.Name))
		}
	case g.GetNextRequest:
		for _, v := range req.Variables {
			out = append(out, a.next(v.Name))
		}
	case g.GetBulkRequest:
		// RFC 3416: non-repeaters once, then the repeaters row by row.
		nr := min(int(req.NonRepeaters), len(req.Variables))
		for _, v := range req.Variables[:nr] {
			out = append(out, a.next(v.Name))
		}
		cur := make([]string, 0, len(req.Variables)-nr)
		for _, v := range req.Variables[nr:] {
			cur = append(cur, v.Name)
		}
		for r := 0; r < int(req.MaxRepetitions) && len(cur) > 0; r++ {
			for j, name := range cur {
				p := a.next(name)
				out = append(out, p)
				cur[j] = p.Name
			}
		}
		if m := a.opts.MaxResponseVarbinds; m > 0 && len(out) > m {
			out = out[:m]
		}
	default:
		return nil
	}
	return out
}

// oidLess compares two dotted OIDs numerically, arc by arc.
func oidLess(a, b string) bool {
	pa := strings.Split(strings.Trim(a, "."), ".")
	pb := strings.Split(strings.Trim(b, "."), ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, _ := strconv.Atoi(pa[i])
		nb, _ := strconv.Atoi(pb[i])
		if na != nb {
			return na < nb
		}
	}
	return len(pa) < len(pb)
}
