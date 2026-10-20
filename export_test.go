package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCSV(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	t0 := time.Date(2026, 10, 20, 17, 23, 5, 0, loc)
	var h History
	h.append(Sample{T: t0, InBps: 1234.6, OutBps: 10e9, InPps: 12.34, OutPps: 0})
	h.append(Sample{T: t0.Add(5 * time.Second), InBps: 0, OutBps: 1, InPps: 0.05, OutPps: 3})
	var b strings.Builder
	if err := writeCSV(&b, h); err != nil {
		t.Fatal(err)
	}
	f := func(t time.Time) string { return t.Local().Format(time.RFC3339) }
	want := "time,in_bps,out_bps,in_pps,out_pps\n" +
		f(t0) + ",1235,10000000000,12.3,0.0\n" +
		f(t0.Add(5*time.Second)) + ",0,1,0.1,3.0\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestCSVFileName(t *testing.T) {
	ts := time.Date(2026, 10, 20, 17, 23, 5, 0, time.Local)
	cases := []struct{ host, ifName, want string }{
		{"router-1", "1/1/1", "tsnmp_router-1_1_1_1_20261020-172305.csv"},
		{"127.0.0.1:16161", `Gi0/0:"x"*?<>|\`, "tsnmp_127.0.0.1_16161_Gi0_0__x________20261020-172305.csv"},
		{"::1", "ethernet ü", "tsnmp___1_ethernet_ü_20261020-172305.csv"},
	}
	for _, c := range cases {
		if got := csvFileName(c.host, c.ifName, ts); got != c.want {
			t.Errorf("csvFileName(%q, %q) = %q want %q", c.host, c.ifName, got, c.want)
		}
	}
}

func TestExportCSVNeverOverwrites(t *testing.T) {
	t.Chdir(t.TempDir())
	ifc := Iface{Name: "eth0", Hist: histN(3)}
	now := time.Date(2026, 10, 20, 17, 23, 5, 0, time.Local)
	var paths []string
	for i := 0; i < 3; i++ {
		p, err := exportCSV(&ifc, "host", now)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Base(p))
	}
	want := []string{
		"tsnmp_host_eth0_20261020-172305.csv",
		"tsnmp_host_eth0_20261020-172305-2.csv",
		"tsnmp_host_eth0_20261020-172305-3.csv",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("file %d = %s want %s", i, paths[i], want[i])
		}
	}
	data, err := os.ReadFile(want[0])
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 4 {
		t.Errorf("%d lines, want header + 3", n)
	}
}
