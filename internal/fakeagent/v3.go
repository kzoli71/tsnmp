package fakeagent

import (
	"crypto/rand"
	"errors"
	"strings"
	"time"

	g "github.com/gosnmp/gosnmp"
)

// V3User is the agent's SNMPv3 user. Auth NoAuth (or 0) means
// noAuthNoPriv, Priv NoPriv (or 0) authNoPriv; requests must use exactly
// this security level and context name.
type V3User struct {
	Name     string
	Auth     g.SnmpV3AuthProtocol
	AuthPass string
	Priv     g.SnmpV3PrivProtocol
	PrivPass string
	Context  string // the only accepted context name ("" = default context)
}

// EngineID is the agent's snmpEngineID (RFC 3411 format: net-snmp
// enterprise number, text).
const EngineID = "\x80\x00\x1f\x88\x04fakeagent"

// USM statistics sent in Report PDUs (RFC 3414).
const (
	oidUnsupportedSecLevels = ".1.3.6.1.6.3.15.1.1.1.0"
	oidNotInTimeWindows     = ".1.3.6.1.6.3.15.1.1.2.0"
	oidUnknownUserNames     = ".1.3.6.1.6.3.15.1.1.3.0"
	oidUnknownEngineIDs     = ".1.3.6.1.6.3.15.1.1.4.0"
	oidWrongDigests         = ".1.3.6.1.6.3.15.1.1.5.0"
	oidDecryptionErrors     = ".1.3.6.1.6.3.15.1.1.6.0"
	oidUnknownContexts      = ".1.3.6.1.6.3.12.1.1.5.0" // RFC 3413
)

// timeWindow is the allowed engine time difference (RFC 3414 3.2.7).
const timeWindow = 150

// v3Engine holds the USM state. Only the serve goroutine uses it.
type v3Engine struct {
	user  V3User
	level g.SnmpV3MsgFlags
	usm   *g.UsmSecurityParameters // the user's parameters with localized keys
	dec   *g.GoSNMP                // decodes, authenticates and decrypts requests
	stats map[string]uint32        // report counters
}

func newV3Engine(u V3User) (*v3Engine, error) {
	u.Auth, u.Priv = max(u.Auth, g.NoAuth), max(u.Priv, g.NoPriv)
	level := g.NoAuthNoPriv
	if u.Auth > g.NoAuth {
		level = g.AuthNoPriv
		if u.Priv > g.NoPriv {
			level = g.AuthPriv
		}
	}
	logger := g.NewLogger(nil)
	sp := &g.UsmSecurityParameters{AuthoritativeEngineID: EngineID, UserName: u.Name,
		AuthenticationProtocol: u.Auth, AuthenticationPassphrase: u.AuthPass,
		PrivacyProtocol: u.Priv, PrivacyPassphrase: u.PrivPass, Logger: logger}
	if err := sp.InitSecurityKeys(); err != nil {
		return nil, err
	}
	return &v3Engine{user: u, level: level, usm: sp, stats: map[string]uint32{},
		dec: &g.GoSNMP{Version: g.Version3, SecurityModel: g.UserSecurityModel, SecurityParameters: sp, Logger: logger}}, nil
}

// serveV3 answers one SNMPv3 message (nil: no answer). Like a real agent it
// sends Report PDUs for engine ID discovery, an unknown user, a wrong
// security level, a wrong digest, a decryption error and an engine
// boots/time outside the time window (e.g. after Reboot) and an unknown
// context name.
func (a *Agent) serveV3(msg []byte) []byte {
	e := a.v3
	h, err := parseV3Header(msg)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	down, boots, etime := a.down, a.boots, uint32(time.Since(a.boot)/time.Second)
	a.mu.Unlock()
	if down {
		return nil
	}
	report := func(oid string, flags g.SnmpV3MsgFlags) []byte {
		return e.report(h, oid, flags, boots, etime)
	}
	switch {
	case h.engineID != EngineID:
		return report(oidUnknownEngineIDs, g.NoAuthNoPriv) // discovery
	case h.user != e.user.Name:
		return report(oidUnknownUserNames, g.NoAuthNoPriv)
	case h.flags&g.AuthPriv != e.level:
		return report(oidUnsupportedSecLevels, g.NoAuthNoPriv)
	}
	req, err := e.dec.UnmarshalTrap(msg, true) // any PDU type, not just traps
	if err != nil {
		if strings.Contains(err.Error(), "not authentic") {
			return report(oidWrongDigests, g.NoAuthNoPriv)
		}
		if e.level == g.AuthPriv {
			return report(oidDecryptionErrors, g.NoAuthNoPriv)
		}
		return nil
	}
	if e.level != g.NoAuthNoPriv && (h.boots != boots || absDiff(h.time, etime) > timeWindow) {
		return report(oidNotInTimeWindows, g.AuthNoPriv)
	}
	if req.ContextName != e.user.Context {
		return report(oidUnknownContexts, g.NoAuthNoPriv)
	}
	if a.ignored(req) {
		return nil
	}
	out := a.handle(req)
	if out == nil {
		return nil
	}
	resp := &g.SnmpPacket{Version: g.Version3, MsgFlags: e.level, SecurityModel: g.UserSecurityModel,
		SecurityParameters: e.params(boots, etime), MsgID: h.msgID, ContextEngineID: EngineID,
		ContextName: req.ContextName, PDUType: g.GetResponse, RequestID: req.RequestID,
		Variables: out, Logger: e.usm.Logger}
	b, err := resp.MarshalMsg()
	if err != nil {
		return nil
	}
	return b
}

// params returns the user's security parameters for an outgoing message.
func (e *v3Engine) params(boots, etime uint32) *g.UsmSecurityParameters {
	sp := e.usm.Copy().(*g.UsmSecurityParameters)
	sp.AuthoritativeEngineBoots, sp.AuthoritativeEngineTime = boots, etime
	if e.level == g.AuthPriv {
		sp.PrivacyParameters = make([]byte, 8) // salt
		rand.Read(sp.PrivacyParameters)
	}
	return sp
}

// report builds a Report PDU with one USM counter; flags is NoAuthNoPriv
// or AuthNoPriv (authenticated with the user's key).
func (e *v3Engine) report(h v3Header, oid string, flags g.SnmpV3MsgFlags, boots, etime uint32) []byte {
	e.stats[oid]++
	var sp *g.UsmSecurityParameters
	if flags&g.AuthNoPriv != 0 {
		sp = e.params(boots, etime)
	} else {
		sp = &g.UsmSecurityParameters{AuthoritativeEngineID: EngineID, AuthoritativeEngineBoots: boots,
			AuthoritativeEngineTime: etime, UserName: h.user, Logger: e.usm.Logger}
	}
	p := &g.SnmpPacket{Version: g.Version3, MsgFlags: flags, SecurityModel: g.UserSecurityModel,
		SecurityParameters: sp, MsgID: h.msgID, ContextEngineID: EngineID, PDUType: g.Report,
		Variables: []g.SnmpPDU{{Name: oid, Type: g.Counter32, Value: e.stats[oid]}}, Logger: e.usm.Logger}
	b, err := p.MarshalMsg()
	if err != nil {
		return nil
	}
	return b
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// v3Header is the plaintext part of an SNMPv3 message that decides how to
// process it.
type v3Header struct {
	msgID       uint32
	flags       g.SnmpV3MsgFlags
	engineID    string
	boots, time uint32
	user        string
}

// berReader reads consecutive BER TLVs (definite lengths only).
type berReader struct{ b []byte }

func (r *berReader) next() (tag byte, val []byte, ok bool) {
	if len(r.b) < 2 {
		return 0, nil, false
	}
	tag, l, p := r.b[0], int(r.b[1]), 2
	if l&0x80 != 0 {
		n := l & 0x7f
		if n == 0 || n > 4 || len(r.b) < 2+n {
			return 0, nil, false
		}
		l = 0
		for _, c := range r.b[2 : 2+n] {
			l = l<<8 | int(c)
		}
		p += n
	}
	if l < 0 || len(r.b) < p+l {
		return 0, nil, false
	}
	val, r.b = r.b[p:p+l], r.b[p+l:]
	return tag, val, true
}

// berUint decodes a non-negative INTEGER.
func berUint(v []byte) uint32 {
	var n uint32
	for _, c := range v {
		n = n<<8 | uint32(c)
	}
	return n
}

var errBadV3 = errors.New("malformed SNMPv3 message")

// msgBody returns the content of the outer message SEQUENCE.
func msgBody(msg []byte) (berReader, bool) {
	outer := berReader{msg}
	tag, body, ok := outer.next()
	return berReader{body}, ok && tag == 0x30
}

// msgVersion returns the version field of an SNMP message (255 if unknown).
func msgVersion(msg []byte) g.SnmpVersion {
	r, ok := msgBody(msg)
	if !ok {
		return 255
	}
	tag, v, ok := r.next()
	if !ok || tag != 0x02 || len(v) != 1 {
		return 255
	}
	return g.SnmpVersion(v[0])
}

func parseV3Header(msg []byte) (v3Header, error) {
	var h v3Header
	r, ok := msgBody(msg)
	if !ok {
		return h, errBadV3
	}
	field := func(rd *berReader, tag byte) []byte {
		t, v, good := rd.next()
		if !good || t != tag {
			ok = false
		}
		return v
	}
	field(&r, 0x02) // version
	global := berReader{field(&r, 0x30)}
	h.msgID = berUint(field(&global, 0x02))
	field(&global, 0x02) // msgMaxSize
	if fl := field(&global, 0x04); len(fl) == 1 {
		h.flags = g.SnmpV3MsgFlags(fl[0])
	}
	secOuter := berReader{field(&r, 0x04)}
	sec := berReader{field(&secOuter, 0x30)}
	h.engineID = string(field(&sec, 0x04))
	h.boots = berUint(field(&sec, 0x02))
	h.time = berUint(field(&sec, 0x02))
	h.user = string(field(&sec, 0x04))
	if !ok {
		return h, errBadV3
	}
	return h, nil
}
