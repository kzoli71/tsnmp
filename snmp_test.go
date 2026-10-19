package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"

	"tsnmp/internal/fakeagent"
)

// startAgent runs a fake agent on a random local port and connects to it.
func startAgent(t *testing.T, extra int) (*fakeagent.Agent, *Client) {
	t.Helper()
	// Truncate bulk responses mid-row, like an agent with a small message size.
	ag, err := fakeagent.Start("127.0.0.1:0", "public", fakeagent.DefaultIfaces(extra),
		fakeagent.Options{MaxResponseVarbinds: 17})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ag.Close)
	c, err := NewClient(v2cConfig(ag))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return ag, c
}

// v2cConfig connects to ag with SNMPv2c, community "public".
func v2cConfig(ag *fakeagent.Agent) Config {
	return Config{Host: "127.0.0.1", Port: uint16(ag.Addr().Port), Version: gosnmp.Version2c, Community: "public"}
}

func TestClientDiscoverAndPoll(t *testing.T) {
	const extra = 150
	_, c := startAgent(t, extra)

	if name, err := c.SysName(); err != nil || name != "fake-router" {
		t.Fatalf("SysName = %q, %v", name, err)
	}
	infos, err := c.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 4+extra {
		t.Fatalf("discovered %d interfaces, want %d", len(infos), 4+extra)
	}
	byIdx := map[int]IfaceInfo{}
	for _, in := range infos {
		byIdx[in.Index] = in
	}
	if in := byIdx[2]; in.Name != "eth0" || in.Speed != 1e9 || in.Admin != 1 {
		t.Errorf("eth0 = %+v", in)
	}
	if in := byIdx[4]; in.Speed != 100e6 || in.Oper != 2 {
		t.Errorf("old0 = %+v", in)
	}
	if in := byIdx[3]; in.Admin != 2 {
		t.Errorf("eth1 admin = %d", in.Admin)
	}

	d, err := c.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Status) != 4+extra || len(d.Counters) != 4+extra {
		t.Fatalf("poll: %d statuses, %d counters", len(d.Status), len(d.Counters))
	}
	if rc := d.Counters[2]; !rc.Is64Bit || !rc.HasUptime || rc.InOct == 0 {
		t.Errorf("eth0 counters = %+v", rc)
	}
	if rc := d.Counters[4]; rc.Is64Bit || rc.InOct == 0 {
		t.Errorf("old0 should use 32-bit counters: %+v", rc)
	}
	if st := d.Status[3]; st.Admin != 2 || st.Oper != 2 {
		t.Errorf("eth1 status = %+v", st)
	}
}

func TestPollRatesAndDiscontinuities(t *testing.T) {
	ag, c := startAgent(t, 3)
	s := NewStore()
	infos, err := c.Discover()
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateInterfaces(infos)
	poll := func() {
		t.Helper()
		d, err := c.Poll()
		if err != nil {
			t.Fatal(err)
		}
		s.ApplyPoll(d)
	}
	samples := func(idx int) int { ifc, _ := s.Get(idx); return ifc.Hist.Len() }

	poll()
	time.Sleep(1100 * time.Millisecond)
	poll()
	for _, idx := range []int{2, 4} { // 64-bit and 32-bit interface
		ifc, _ := s.Get(idx)
		smp, ok := ifc.Latest()
		if !ok || smp.InBps <= 0 || smp.InBps > float64(ifc.Speed) {
			t.Errorf("ifIndex %d rate = %+v, %v", idx, smp, ok)
		}
	}

	// Counter reset with a new discontinuity time: that sample is dropped.
	before := samples(4)
	ag.ResetCounters(4)
	time.Sleep(1100 * time.Millisecond)
	poll()
	if samples(4) != before {
		t.Errorf("sample across a counter reset was kept")
	}
	// Agent reboot (sysUpTime restarts): every interface skips one sample.
	b2, b4 := samples(2), samples(4)
	ag.Reboot()
	time.Sleep(1100 * time.Millisecond)
	poll()
	if samples(2) != b2 || samples(4) != b4 {
		t.Errorf("sample across agent reboot was kept: %d->%d, %d->%d", b2, samples(2), b4, samples(4))
	}
	// After the reboot the next poll measures again.
	time.Sleep(1100 * time.Millisecond)
	poll()
	if samples(2) != b2+1 {
		t.Errorf("no sample after reboot recovery")
	}
}

// fastFail shortens timeouts so tests of an unreachable agent are quick.
func fastFail(c *Client) {
	c.snmp.Timeout = 300 * time.Millisecond
	c.snmp.Retries = 0
}

func TestPollerDiscoveryFailureKeepsPolling(t *testing.T) {
	ag, c := startAgent(t, 3)
	fastFail(c)
	s := NewStore()
	p := NewPoller(c, s, nil)

	if res := p.cycle(); res.Err != nil || res.DiscErr != nil {
		t.Fatalf("first cycle: %+v", res)
	}
	// Rediscovery is due but discovery requests (ifAlias) get no answer.
	ag.SetDropPrefix(oidIfAlias)
	p.nextDiscAt = time.Time{}
	time.Sleep(1100 * time.Millisecond)
	res := p.cycle()
	if res.DiscErr == nil || res.Err != nil {
		t.Fatalf("discovery failure: %+v", res)
	}
	if ifc, _ := s.Get(2); ifc.Hist.Len() != 1 {
		t.Errorf("polling stopped while discovery failed: %d samples", ifc.Hist.Len())
	}
	// Backoff: the next cycle does not retry discovery (no timeout wait).
	start := time.Now()
	res = p.cycle()
	if d := time.Since(start); d > 200*time.Millisecond || res.Err != nil || res.DiscErr == nil {
		t.Errorf("cycle during backoff took %v: %+v", d, res)
	}
	// Discovery works again once the backoff is over.
	ag.SetDropPrefix("")
	p.nextDiscAt = time.Time{}
	if res := p.cycle(); res.DiscErr != nil || res.Err != nil {
		t.Errorf("after recovery: %+v", res)
	}
}

func TestPollerConnectionLost(t *testing.T) {
	ag, c := startAgent(t, 3)
	fastFail(c)
	p := NewPoller(c, NewStore(), nil)
	p.cycle()
	ag.SetDown(true)
	if res := p.cycle(); res.Err == nil {
		t.Error("no error while the agent is down")
	}
	ag.SetDown(false)
	if res := p.cycle(); res.Err != nil {
		t.Errorf("after reconnect: %v", res.Err)
	}
	// Before any successful discovery there is nothing to poll: error.
	ag.SetDown(true)
	p2 := NewPoller(c, NewStore(), nil)
	if res := p2.cycle(); res.Err == nil {
		t.Error("no error without any discovery")
	}
}

func TestPoll32BitFallbackPaths(t *testing.T) {
	for _, n := range []int{3, maxTargetedIfaces + 5} { // targeted GETs, table walk
		ifs := map[int]fakeagent.Iface{1: {Name: "hc", Admin: 1, Oper: 1, HC: true, Bps: 1e6, SpeedMbps: 10000}}
		for i := 0; i < n; i++ {
			ifs[100+i] = fakeagent.Iface{Name: fmt.Sprint("old", i), Admin: 1, Oper: 1, Bps: 1e6, SpeedMbps: 100}
		}
		ag, err := fakeagent.Start("127.0.0.1:0", "public", ifs, fakeagent.Options{MaxResponseVarbinds: 23})
		if err != nil {
			t.Fatal(err)
		}
		c, err := NewClient(v2cConfig(ag))
		if err != nil {
			t.Fatal(err)
		}
		infos, err := c.Discover()
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range infos {
			// 10 Gb/s: ifSpeed saturates at 4294967295, ifHighSpeed is used.
			if in.Index == 1 && in.Speed != 10_000_000_000 {
				t.Errorf("10G speed = %d", in.Speed)
			}
		}
		d, err := c.Poll()
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Counters) != n+1 {
			t.Errorf("%d 32-bit interfaces: got counters for %d of %d", n, len(d.Counters), n+1)
		}
		for idx, rc := range d.Counters {
			if idx != 1 && (rc.Is64Bit || !rc.HasUptime || rc.InOct == 0) {
				t.Errorf("ifIndex %d counters = %+v", idx, rc)
			}
		}
		c.Close()
		ag.Close()
	}
}

func TestIndexFromOID(t *testing.T) {
	for _, base := range []string{".1.3.6.1.2.1.2.2.1.10", "1.3.6.1.2.1.2.2.1.10"} {
		if idx, ok := indexFromOID(".1.3.6.1.2.1.2.2.1.10.123", base); !ok || idx != 123 {
			t.Errorf("base %q: %d, %v", base, idx, ok)
		}
	}
	if _, ok := indexFromOID(".1.3.6.1.2.1.2.2.1.1.5", ".1.3.6.1.2.1.2.2.1.10"); ok {
		t.Error("column .1 matched as .10")
	}
}
