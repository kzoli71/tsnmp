// Package usm maps SNMPv3 USM protocol names (net-snmp style) to gosnmp
// constants, for tsnmp and the fake agent.
package usm

import (
	"strings"

	g "github.com/gosnmp/gosnmp"
)

// AuthNames lists the authentication protocols; AuthNames[i] is gosnmp
// protocol i+1 (NoAuth = 1).
var AuthNames = []string{"none", "MD5", "SHA", "SHA224", "SHA256", "SHA384", "SHA512"}

// PrivNames lists the privacy protocols; PrivNames[i] is gosnmp protocol
// i+1 (NoPriv = 1). AES192C / AES256C are the Reeder ("Cisco") key
// extension variants, AES192 / AES256 the Blumenthal ones.
var PrivNames = []string{"none", "DES", "AES", "AES192", "AES256", "AES192C", "AES256C"}

// normalize makes "sha-256", "SHA1" or "aes128" match the listed names.
func normalize(s string) string {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	switch s {
	case "", "NONE":
		return "NONE"
	case "SHA1":
		return "SHA"
	case "AES128":
		return "AES"
	}
	return s
}

func index(names []string, s string) int {
	s = normalize(s)
	for i, n := range names {
		if strings.ToUpper(n) == s {
			return i
		}
	}
	return -1
}

// Auth parses an authentication protocol name ("" and "none": NoAuth).
func Auth(s string) (g.SnmpV3AuthProtocol, bool) {
	i := index(AuthNames, s)
	return g.SnmpV3AuthProtocol(i + 1), i >= 0
}

// Priv parses a privacy protocol name ("" and "none": NoPriv).
func Priv(s string) (g.SnmpV3PrivProtocol, bool) {
	i := index(PrivNames, s)
	return g.SnmpV3PrivProtocol(i + 1), i >= 0
}

// AuthName returns the name of an authentication protocol.
func AuthName(p g.SnmpV3AuthProtocol) string {
	if p < 1 || int(p) > len(AuthNames) {
		return "none"
	}
	return AuthNames[p-1]
}

// PrivName returns the name of a privacy protocol.
func PrivName(p g.SnmpV3PrivProtocol) string {
	if p < 1 || int(p) > len(PrivNames) {
		return "none"
	}
	return PrivNames[p-1]
}
