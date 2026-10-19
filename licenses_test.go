package main

import (
	"os"
	"strings"
	"testing"
)

// TestThirdPartyLicenses checks that the embedded license list is not empty
// and names every module required in go.mod (all of them are compiled in).
func TestThirdPartyLicenses(t *testing.T) {
	if !strings.Contains(thirdPartyLicenses, "\nGo standard library ") {
		t.Fatal("THIRD_PARTY_LICENSES.txt: Go standard library missing")
	}
	if !strings.HasPrefix(licensesText(), "tsnmp\n") || !strings.Contains(licensesText(), "MIT License") {
		t.Fatal("licensesText: own license missing")
	}
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	inRequire, n := false, 0
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimSpace(strings.SplitN(line, "//", 2)[0]))
		switch {
		case len(f) == 2 && f[0] == "require" && f[1] == "(":
			inRequire = true
			continue
		case inRequire && len(f) == 1 && f[0] == ")":
			inRequire = false
			continue
		case len(f) == 3 && f[0] == "require":
			f = f[1:]
		case !inRequire || len(f) != 2:
			continue
		}
		n++
		if !strings.Contains(thirdPartyLicenses, "\n"+f[0]+" "+f[1]+"\n") {
			t.Errorf("THIRD_PARTY_LICENSES.txt: %s %s missing; run: go run ./tools/licenses -write", f[0], f[1])
		}
	}
	if n == 0 {
		t.Fatal("no requires found in go.mod")
	}
}
