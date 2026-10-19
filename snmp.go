package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"tsnmp/internal/usm"
)

const (
	oidSysName   = ".1.3.6.1.2.1.1.5.0"
	oidSysUpTime = ".1.3.6.1.2.1.1.3" // GETNEXT/bulk non-repeater -> sysUpTime.0

	oidIfDescr       = ".1.3.6.1.2.1.2.2.1.2"
	oidIfAdminStatus = ".1.3.6.1.2.1.2.2.1.7"
	oidIfOperStatus  = ".1.3.6.1.2.1.2.2.1.8"
	oidIfName        = ".1.3.6.1.2.1.31.1.1.1.1"
	oidIfAlias       = ".1.3.6.1.2.1.31.1.1.1.18"
	oidIfSpeed       = ".1.3.6.1.2.1.2.2.1.5"     // bit/s, saturates at 4.29 Gb/s
	oidIfHighSpeed   = ".1.3.6.1.2.1.31.1.1.1.15" // Mbit/s
	oidIfCounterDisc = ".1.3.6.1.2.1.31.1.1.1.19" // ifCounterDiscontinuityTime

	// 32-bit counters (IF-MIB ifTable)
	oidIfInOctets      = ".1.3.6.1.2.1.2.2.1.10"
	oidIfInUcastPkts   = ".1.3.6.1.2.1.2.2.1.11"
	oidIfInNUcastPkts  = ".1.3.6.1.2.1.2.2.1.12"
	oidIfOutOctets     = ".1.3.6.1.2.1.2.2.1.16"
	oidIfOutUcastPkts  = ".1.3.6.1.2.1.2.2.1.17"
	oidIfOutNUcastPkts = ".1.3.6.1.2.1.2.2.1.18"

	// 64-bit counters (IF-MIB ifXTable)
	oidIfHCInOctets        = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCInUcastPkts     = ".1.3.6.1.2.1.31.1.1.1.7"
	oidIfHCInMulticastPkts = ".1.3.6.1.2.1.31.1.1.1.8"
	oidIfHCInBcastPkts     = ".1.3.6.1.2.1.31.1.1.1.9"
	oidIfHCOutOctets       = ".1.3.6.1.2.1.31.1.1.1.10"
	oidIfHCOutUcastPkts    = ".1.3.6.1.2.1.31.1.1.1.11"
	oidIfHCOutMcastPkts    = ".1.3.6.1.2.1.31.1.1.1.12"
	oidIfHCOutBcastPkts    = ".1.3.6.1.2.1.31.1.1.1.13"
)

// Counter column order: InOct, OutOct, InPkts[0..2], OutPkts[0..2].
var (
	hcColumns = []string{oidIfHCInOctets, oidIfHCOutOctets,
		oidIfHCInUcastPkts, oidIfHCInMulticastPkts, oidIfHCInBcastPkts,
		oidIfHCOutUcastPkts, oidIfHCOutMcastPkts, oidIfHCOutBcastPkts}
	c32Columns = []string{oidIfInOctets, oidIfOutOctets,
		oidIfInUcastPkts, oidIfInNUcastPkts, "",
		oidIfOutUcastPkts, oidIfOutNUcastPkts, ""}
)

// Config describes how to reach the agent.
type Config struct {
	Host      string
	Port      uint16
	Version   gosnmp.SnmpVersion // gosnmp.Version2c or gosnmp.Version3
	Community string             // v2c

	// v3 (USM). The security level follows from the protocols: Auth
	// NoAuth = noAuthNoPriv, Priv NoPriv = authNoPriv, else authPriv.
	User     string
	Auth     gosnmp.SnmpV3AuthProtocol
	AuthPass string
	Priv     gosnmp.SnmpV3PrivProtocol
	PrivPass string
	Context  string // v3 context name ("" = default context)
}

// minPassLen is the shortest USM passphrase (RFC 3414 section 11.2).
const minPassLen = 8

// secLevel returns the v3 security level flags.
func (c Config) secLevel() gosnmp.SnmpV3MsgFlags {
	switch {
	case c.Auth <= gosnmp.NoAuth:
		return gosnmp.NoAuthNoPriv
	case c.Priv <= gosnmp.NoPriv:
		return gosnmp.AuthNoPriv
	}
	return gosnmp.AuthPriv
}

// Validate checks the v3 settings.
func (c Config) Validate() error {
	if c.Version != gosnmp.Version3 {
		return nil
	}
	switch {
	case c.User == "":
		return errors.New("SNMPv3 user name is required")
	case c.Auth <= gosnmp.NoAuth && c.Priv > gosnmp.NoPriv:
		return errors.New("privacy (encryption) requires an authentication protocol")
	case c.Auth > gosnmp.NoAuth && len(c.AuthPass) < minPassLen:
		return fmt.Errorf("the authentication password must be at least %d characters", minPassLen)
	case c.Priv > gosnmp.NoPriv && len(c.PrivPass) < minPassLen:
		return fmt.Errorf("the privacy password must be at least %d characters", minPassLen)
	}
	return nil
}

// Describe is a short text of the SNMP version and security settings.
func (c Config) Describe() string {
	if c.Version != gosnmp.Version3 {
		return "v2c"
	}
	var s string
	switch c.secLevel() {
	case gosnmp.NoAuthNoPriv:
		s = fmt.Sprintf("v3 %s, noAuthNoPriv", c.User)
	case gosnmp.AuthNoPriv:
		s = fmt.Sprintf("v3 %s, authNoPriv %s", c.User, usm.AuthName(c.Auth))
	default:
		s = fmt.Sprintf("v3 %s, authPriv %s/%s", c.User, usm.AuthName(c.Auth), usm.PrivName(c.Priv))
	}
	if c.Context != "" {
		s += fmt.Sprintf(", context %q", c.Context)
	}
	return s
}

// Short is the SNMP version and security level.
func (c Config) Short() string {
	if c.Version != gosnmp.Version3 {
		return "v2c"
	}
	switch c.secLevel() {
	case gosnmp.NoAuthNoPriv:
		return "v3 noAuthNoPriv"
	case gosnmp.AuthNoPriv:
		return "v3 authNoPriv"
	}
	return "v3 authPriv"
}

// Client wraps an SNMP session. It is not safe for concurrent use.
type Client struct {
	snmp *gosnmp.GoSNMP
}

func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	g := &gosnmp.GoSNMP{
		Target:    cfg.Host,
		Port:      cfg.Port,
		Community: cfg.Community,
		Version:   gosnmp.Version2c,
		Timeout:   2 * time.Second,
		Retries:   1,
	}
	if cfg.Version == gosnmp.Version3 {
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = cfg.secLevel()
		g.ContextName = cfg.Context
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 cfg.User,
			AuthenticationProtocol:   max(cfg.Auth, gosnmp.NoAuth),
			AuthenticationPassphrase: cfg.AuthPass,
			PrivacyProtocol:          max(cfg.Priv, gosnmp.NoPriv),
			PrivacyPassphrase:        cfg.PrivPass,
		}
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	return &Client{snmp: g}, nil
}

// v3Errors explains the gosnmp errors of SNMPv3 report PDUs.
var v3Errors = []struct {
	err  error
	text string
}{
	{gosnmp.ErrUnknownUsername, "SNMPv3: unknown user name"},
	{gosnmp.ErrWrongDigest, "SNMPv3: authentication failed (wrong authentication password or protocol)"},
	{gosnmp.ErrDecryption, "SNMPv3: decryption failed (wrong privacy password or protocol)"},
	{gosnmp.ErrUnknownSecurityLevel, "SNMPv3: security level not supported for this user"},
	{gosnmp.ErrNotInTimeWindow, "SNMPv3: not in time window"},
	{gosnmp.ErrUnknownEngineID, "SNMPv3: unknown engine ID"},
	// e.g. snmpUnknownContexts, which gosnmp does not name
	{gosnmp.ErrUnknownReportPDU, "SNMPv3: request rejected by the agent (check the context name)"},
}

// snmpErr makes SNMPv3 errors readable; other errors are returned as is.
func snmpErr(err error) error {
	for _, e := range v3Errors {
		if errors.Is(err, e.err) {
			return errors.New(e.text)
		}
	}
	return err
}

func (c *Client) Close() {
	if c.snmp.Conn != nil {
		_ = c.snmp.Conn.Close()
	}
}

// SysName queries sysName.0.
func (c *Client) SysName() (string, error) {
	res, err := c.snmp.Get([]string{oidSysName})
	if err != nil {
		return "", snmpErr(err)
	}
	if len(res.Variables) == 0 {
		return "", fmt.Errorf("empty response")
	}
	v := res.Variables[0]
	if b, ok := v.Value.([]byte); ok {
		return string(b), nil
	}
	return "", fmt.Errorf("sysName not available")
}

// maxBulkVarbinds is the number of varbinds requested per GetBulk; agents
// may return fewer (message size limit), which walkTable handles.
const maxBulkVarbinds = 60

// stamp is when a value was read: local time and the agent's sysUpTime
// (centiseconds) from the same response.
type stamp struct {
	T         time.Time
	Uptime    uint32
	HasUptime bool
}

// table is the result of walkTable: per column ifIndex -> value, and per
// ifIndex the stamp of the response that carried the row's first column.
type table struct {
	cols  []map[int]gosnmp.SnmpPDU
	stamp map[int]stamp
}

// walkTable walks several ifTable/ifXTable columns side by side with
// GetBulk, so a row's values come from the same response. Every request also
// fetches sysUpTime as a non-repeater, giving the agent's own time for the
// rows of that response. Empty column OIDs are skipped (nil map).
func (c *Client) walkTable(cols []string) (table, error) {
	t := table{cols: make([]map[int]gosnmp.SnmpPDU, len(cols)), stamp: map[int]stamp{}}
	cur := map[int]string{} // column -> last OID seen
	for i, col := range cols {
		if col != "" {
			t.cols[i] = map[int]gosnmp.SnmpPDU{}
			cur[i] = col
		}
	}
	for len(cur) > 0 {
		active := make([]int, 0, len(cur))
		for i := range cur {
			active = append(active, i)
		}
		sort.Ints(active)
		oids := []string{oidSysUpTime}
		for _, i := range active {
			oids = append(oids, cur[i])
		}
		res, err := c.snmp.GetBulk(oids, 1, uint32(max(maxBulkVarbinds/len(active), 1)))
		if err != nil {
			return t, snmpErr(err)
		}
		vbs := res.Variables
		if len(vbs) == 0 {
			return t, fmt.Errorf("empty GetBulk response")
		}
		st := stamp{T: time.Now()}
		if vbs[0].Type == gosnmp.TimeTicks {
			st.Uptime, st.HasUptime = uint32(pduUint64(vbs[0])), true
		}
		progressed := false
		for k, p := range vbs[1:] {
			i := active[k%len(active)]
			if _, ok := cur[i]; !ok {
				continue // column already finished earlier in this response
			}
			idx, ok := indexFromOID(p.Name, cols[i])
			if !ok || isMissing(p) {
				delete(cur, i) // left the column (or end of MIB)
				continue
			}
			if t.cols[i][idx].Name == p.Name {
				delete(cur, i) // no progress: broken agent, avoid looping
				continue
			}
			t.cols[i][idx] = p
			cur[i] = p.Name
			progressed = true
			if i == active[0] {
				if _, seen := t.stamp[idx]; !seen {
					t.stamp[idx] = st
				}
			}
		}
		if !progressed && len(cur) > 0 {
			return t, fmt.Errorf("GetBulk response does not advance")
		}
	}
	return t, nil
}

func indexFromOID(name, base string) (int, bool) {
	prefix := "." + strings.TrimPrefix(base, ".") + "."
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(name[len(prefix):])
	return n, err == nil
}

func pduString(p gosnmp.SnmpPDU) string {
	if b, ok := p.Value.([]byte); ok {
		return string(b)
	}
	return ""
}

// pduInt is for small enumerations (admin/oper status).
func pduInt(p gosnmp.SnmpPDU) int {
	switch p.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.Counter64, gosnmp.Uinteger32, gosnmp.TimeTicks:
		return int(gosnmp.ToBigInt(p.Value).Int64())
	}
	return 0
}

// pduUint64 is for unsigned values that may not fit a 32-bit int (ifSpeed
// up to 4294967295, TimeTicks), so it is safe on 32-bit platforms too.
func pduUint64(p gosnmp.SnmpPDU) uint64 {
	switch p.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.Counter64, gosnmp.Uinteger32, gosnmp.TimeTicks:
		return gosnmp.ToBigInt(p.Value).Uint64()
	}
	return 0
}

// Discover reads the interface list with names, descriptions, status and
// speed in one table walk.
func (c *Client) Discover() ([]IfaceInfo, error) {
	t, err := c.walkTable([]string{oidIfAdminStatus, oidIfDescr, oidIfOperStatus,
		oidIfName, oidIfAlias, oidIfSpeed, oidIfHighSpeed})
	if err != nil {
		return nil, err
	}
	admin, descr, oper, names, alias, speed, high :=
		t.cols[0], t.cols[1], t.cols[2], t.cols[3], t.cols[4], t.cols[5], t.cols[6]

	var out []IfaceInfo
	for idx, a := range admin {
		info := IfaceInfo{
			Index: idx,
			Admin: pduInt(a),
			Descr: pduString(descr[idx]),
			Name:  pduString(names[idx]),
			Alias: pduString(alias[idx]),
			Oper:  pduInt(oper[idx]),
			Speed: pduUint64(speed[idx]),
		}
		// ifSpeed saturates at 4294967295; ifHighSpeed (Mbit/s) is preferred.
		if hs := pduUint64(high[idx]); hs > 0 {
			info.Speed = hs * 1_000_000
		}
		if info.Name == "" {
			info.Name = info.Descr
		}
		out = append(out, info)
	}
	return out, nil
}

func isMissing(p gosnmp.SnmpPDU) bool {
	switch p.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return true
	}
	return false
}

// IfStatus is the admin and oper status of one interface.
type IfStatus struct {
	Admin int
	Oper  int
}

// PollData is the result of one poll cycle.
type PollData struct {
	Status   map[int]IfStatus
	Counters map[int]RawCounters
}

// counterRow assembles counter values for idx from columns starting at
// first; ok is false if any used column is missing.
func counterRow(cols []map[int]gosnmp.SnmpPDU, first, idx int) (vals [8]uint64, ok bool) {
	for i, m := range cols[first : first+8] {
		if m == nil {
			continue // unused column (e.g. 3rd packet part in 32-bit mode)
		}
		p, found := m[idx]
		if !found || isMissing(p) {
			return vals, false
		}
		vals[i] = gosnmp.ToBigInt(p.Value).Uint64()
	}
	return vals, true
}

// Poll walks the status, 64-bit counters and counter discontinuity time of
// all interfaces in one table walk; the 32-bit ifTable counters are walked
// only when some interface lacks HC counters.
func (c *Client) Poll() (PollData, error) {
	d := PollData{Status: map[int]IfStatus{}, Counters: map[int]RawCounters{}}
	// Columns: 0..7 HC counters (first one stamps the row), 8 admin,
	// 9 oper, 10 discontinuity time.
	cols := append(append([]string{}, hcColumns...), oidIfAdminStatus, oidIfOperStatus, oidIfCounterDisc)
	t, err := c.walkTable(cols)
	if err != nil {
		return d, err
	}
	admin, oper, disc := t.cols[8], t.cols[9], t.cols[10]
	var missing []int
	for idx, p := range admin {
		d.Status[idx] = IfStatus{Admin: pduInt(p), Oper: pduInt(oper[idx])}
		v, ok := counterRow(t.cols, 0, idx)
		st, stamped := t.stamp[idx]
		if !ok || !stamped {
			missing = append(missing, idx)
			continue
		}
		d.Counters[idx] = newRawCounters(st, v, true, uint32(pduUint64(disc[idx])))
	}
	if len(missing) > 0 {
		// A few interfaces: targeted GETs; many: walk the 32-bit table.
		fetch := c.walkTable
		if len(missing) <= maxTargetedIfaces {
			fetch = func(cols []string) (table, error) { return c.getRows(cols, missing) }
		}
		t32, err := fetch(c32Columns)
		if err != nil {
			return d, err
		}
		for _, idx := range missing {
			v, ok := counterRow(t32.cols, 0, idx)
			if st, stamped := t32.stamp[idx]; ok && stamped {
				d.Counters[idx] = newRawCounters(st, v, false, uint32(pduUint64(disc[idx])))
			}
		}
	}
	return d, nil
}

const (
	oidSysUpTime0 = ".1.3.6.1.2.1.1.3.0"
	// ifacesPerGet interfaces' 6 counter OIDs plus sysUpTime.0 fit one GET
	// (gosnmp's default MaxOids is 60).
	ifacesPerGet = 9
	// maxTargetedIfaces is the most interfaces fetched with targeted GETs
	// (4 requests) instead of walking the whole 32-bit table.
	maxTargetedIfaces = 4 * ifacesPerGet
)

// getRows fetches the given columns for a few interfaces with GET requests,
// each also asking sysUpTime.0 for the row stamps. The result has the same
// shape as walkTable's; missing values are simply absent.
func (c *Client) getRows(cols []string, indexes []int) (table, error) {
	t := table{cols: make([]map[int]gosnmp.SnmpPDU, len(cols)), stamp: map[int]stamp{}}
	for i, col := range cols {
		if col != "" {
			t.cols[i] = map[int]gosnmp.SnmpPDU{}
		}
	}
	for start := 0; start < len(indexes); start += ifacesPerGet {
		batch := indexes[start:min(start+ifacesPerGet, len(indexes))]
		oids := []string{oidSysUpTime0}
		for _, idx := range batch {
			for _, col := range cols {
				if col != "" {
					oids = append(oids, fmt.Sprintf("%s.%d", col, idx))
				}
			}
		}
		res, err := c.snmp.Get(oids)
		if err != nil {
			return t, snmpErr(err)
		}
		st := stamp{T: time.Now()}
		for _, p := range res.Variables {
			if p.Name == oidSysUpTime0 && p.Type == gosnmp.TimeTicks {
				st.Uptime, st.HasUptime = uint32(pduUint64(p)), true
			}
		}
		for _, p := range res.Variables {
			if isMissing(p) {
				continue
			}
			for i, col := range cols {
				if idx, ok := indexFromOID(p.Name, col); ok && col != "" {
					t.cols[i][idx] = p
					t.stamp[idx] = st
				}
			}
		}
	}
	return t, nil
}

func newRawCounters(st stamp, v [8]uint64, is64 bool, disc uint32) RawCounters {
	return RawCounters{
		T:         st.T,
		Uptime:    st.Uptime,
		HasUptime: st.HasUptime,
		Discont:   disc,
		InOct:     v[0],
		OutOct:    v[1],
		InPkts:    [3]uint64{v[2], v[3], v[4]},
		OutPkts:   [3]uint64{v[5], v[6], v[7]},
		Is64Bit:   is64,
	}
}
