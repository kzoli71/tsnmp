package main

import (
	"fmt"
	"strings"
	"time"
)

// scaleRate scales v to below 1000 and returns the SI prefix to use.
func scaleRate(v float64) (float64, string) {
	prefixes := []string{"", "K", "M", "G", "T"}
	i := 0
	// 999.95 so that rounding to one decimal never shows "1000.0".
	for v >= 999.95 && i < len(prefixes)-1 {
		v /= 1000
		i++
	}
	return v, prefixes[i]
}

// formatRate renders a value with an SI prefix, e.g. 12.3 Mb/s.
func formatRate(v float64, unit string) string {
	v, p := scaleRate(v)
	if p == "" {
		return fmt.Sprintf("%.0f %s", v, unit)
	}
	return fmt.Sprintf("%.1f %s%s", v, p, unit)
}

// rateWidth is the width of formatRateFixed output for a 3-letter unit.
const rateWidth = 10

// formatRateFixed is formatRate padded to a constant width so table columns
// do not change size between polls ("  12.3 Mb/s", "   512  b/s").
func formatRateFixed(v float64, unit string) string {
	v, p := scaleRate(v)
	num := fmt.Sprintf("%.1f", v)
	if p == "" {
		num = fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%5s %1s%s", num, p, unit)
}

// truncate shortens s to at most n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// statusLetter shows an ifAdminStatus/ifOperStatus value as U (up) or D.
func statusLetter(v int) string {
	if v == 1 {
		return "U"
	}
	return "D"
}

// naturalLess compares strings case-insensitively with digit runs compared
// as numbers, so "ge-0/0/9" sorts before "ge-0/0/10".
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digitPrefix(a), digitPrefix(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digitPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}
