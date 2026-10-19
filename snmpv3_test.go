package main

import (
	"strings"
	"testing"
	"time"

	g "github.com/gosnmp/gosnmp"

	"tsnmp/internal/fakeagent"
)

// startV3Agent runs a fake agent with one SNMPv3 user and returns the
// matching client config.
func startV3Agent(t *testing.T, u fakeagent.V3User) (*fakeagent.Agent, Config) {
	t.Helper()
	ag, err := fakeagent.Start("127.0.0.1:0", "public", fakeagent.DefaultIfaces(20),
		fakeagent.Options{MaxResponseVarbinds: 17, V3: &u})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ag.Close)
	return ag, Config{Host: "127.0.0.1", Port: uint16(ag.Addr().Port), Version: g.Version3,
		User: u.Name, Auth: max(u.Auth, g.NoAuth), AuthPass: u.AuthPass, Priv: max(u.Priv, g.NoPriv), PrivPass: u.PrivPass,
		Context: u.Context}
}

func dialV3(t *testing.T, cfg Config) *Client {
	t.Helper()
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	fastFail(c)
	return c
}

func TestV3SecurityLevels(t *testing.T) {
	users := []fakeagent.V3User{
		{Name: "noauth"},
		{Name: "md5", Auth: g.MD5, AuthPass: "authpass1"},
		{Name: "sha512", Auth: g.SHA512, AuthPass: "authpass1"},
		{Name: "des", Auth: g.SHA, AuthPass: "authpass1", Priv: g.DES, PrivPass: "privpass1"},
		{Name: "aes", Auth: g.SHA256, AuthPass: "authpass1", Priv: g.AES, PrivPass: "privpass1"},
		{Name: "aes256", Auth: g.SHA, AuthPass: "authpass1", Priv: g.AES256, PrivPass: "privpass1"},
		{Name: "aes256c", Auth: g.SHA, AuthPass: "authpass1", Priv: g.AES256C, PrivPass: "privpass1"},
		{Name: "context", Auth: g.SHA, AuthPass: "authpass1", Priv: g.AES, PrivPass: "privpass1", Context: "vprn1"},
	}
	for _, u := range users {
		t.Run(u.Name, func(t *testing.T) {
			ag, cfg := startV3Agent(t, u)
			c := dialV3(t, cfg)
			if name, err := c.SysName(); err != nil || name != "fake-router" {
				t.Fatalf("SysName = %q, %v", name, err)
			}
			infos, err := c.Discover()
			if err != nil || len(infos) != 4+20 {
				t.Fatalf("Discover: %d interfaces, %v", len(infos), err)
			}
			d, err := c.Poll()
			if err != nil || len(d.Counters) != 4+20 {
				t.Fatalf("Poll: %d counters, %v", len(d.Counters), err)
			}
			// v2c keeps working on the same agent.
			v2 := dialV3(t, v2cConfig(ag))
			if _, err := v2.SysName(); err != nil {
				t.Errorf("v2c next to v3: %v", err)
			}
		})
	}
}

func TestV3Errors(t *testing.T) {
	_, cfg := startV3Agent(t, fakeagent.V3User{Name: "user", Auth: g.SHA, AuthPass: "authpass1", Priv: g.AES, PrivPass: "privpass1",
		Context: "ctx"})
	cases := []struct {
		name string
		mod  func(*Config)
		want string
	}{
		{"unknown user", func(c *Config) { c.User = "other" }, "unknown user name"},
		{"wrong auth password", func(c *Config) { c.AuthPass = "wrongpass" }, "wrong authentication password"},
		{"wrong auth protocol", func(c *Config) { c.Auth = g.MD5 }, "wrong authentication password"},
		{"wrong privacy password", func(c *Config) { c.PrivPass = "wrongpass" }, "wrong privacy password"},
		{"lower security level", func(c *Config) { c.Priv = g.NoPriv }, "security level not supported"},
		{"wrong context", func(c *Config) { c.Context = "other" }, "check the context name"},
		{"default context", func(c *Config) { c.Context = "" }, "check the context name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			tc.mod(&c)
			_, err := dialV3(t, c).SysName()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestV3AgentReboot(t *testing.T) {
	// After a reboot the agent's engine boots changes: the client gets a
	// notInTimeWindow report and must resynchronize by itself.
	ag, cfg := startV3Agent(t, fakeagent.V3User{Name: "user", Auth: g.SHA, AuthPass: "authpass1", Priv: g.AES, PrivPass: "privpass1"})
	c := dialV3(t, cfg)
	if _, err := c.Poll(); err != nil {
		t.Fatal(err)
	}
	ag.Reboot()
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Poll(); err != nil {
		t.Fatalf("poll after reboot: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	ok := Config{Version: g.Version3, User: "u", Auth: g.SHA, AuthPass: "12345678", Priv: g.AES, PrivPass: "12345678"}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid config: %v", err)
	}
	for name, mod := range map[string]func(*Config){
		"no user":           func(c *Config) { c.User = "" },
		"priv without auth": func(c *Config) { c.Auth = g.NoAuth },
		"short auth pass":   func(c *Config) { c.AuthPass = "1234567" },
		"short priv pass":   func(c *Config) { c.PrivPass = "1234567" },
	} {
		c := ok
		mod(&c)
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if (Config{Version: g.Version2c}).Validate() != nil {
		t.Error("v2c config rejected")
	}
	if got := ok.Describe(); got != "v3 u, authPriv SHA/AES" {
		t.Errorf("Describe = %q", got)
	}
	ok.Context = "vprn1"
	if got := ok.Describe(); got != `v3 u, authPriv SHA/AES, context "vprn1"` {
		t.Errorf("Describe with context = %q", got)
	}
	if got := ok.Short(); got != "v3 authPriv" {
		t.Errorf("Short = %q", got)
	}
}

func TestLoginFromFlags(t *testing.T) {
	pw := loginState{user: "u", authPass: "authpass", privPass: "privpass", context: "ctx"}
	st, err := loginFromFlags("3", "", "", pw)
	if err != nil || !st.v3 || st.auth != g.SHA || st.priv != g.AES || st.context != "ctx" {
		t.Errorf("defaults from passwords: %+v, %v", st, err)
	}
	st, err = loginFromFlags("v3", "sha-256", "aes256c", pw)
	if err != nil || st.auth != g.SHA256 || st.priv != g.AES256C {
		t.Errorf("named protocols: %+v, %v", st, err)
	}
	st, err = loginFromFlags("2c", "", "", loginState{community: "c"})
	if err != nil || st.v3 || st.auth != g.NoAuth || st.priv != g.NoPriv {
		t.Errorf("v2c: %+v, %v", st, err)
	}
	for _, bad := range [][3]string{{"1", "", ""}, {"3", "SHA3", ""}, {"3", "", "3DES"}} {
		if _, err := loginFromFlags(bad[0], bad[1], bad[2], pw); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
