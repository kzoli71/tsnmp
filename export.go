package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// writeCSV writes the samples of h as CSV: a header line, then one line per
// measurement (local time with offset, b/s as integers, p/s with one decimal).
func writeCSV(w io.Writer, h History) error {
	bw := bufio.NewWriter(w)
	b := []byte("time,in_bps,out_bps,in_pps,out_pps\n")
	if _, err := bw.Write(b); err != nil {
		return err
	}
	for i := 0; i < h.Len(); i++ {
		s := h.At(i)
		b = s.T.Local().AppendFormat(b[:0], time.RFC3339)
		b = append(b, ',')
		b = strconv.AppendFloat(b, s.InBps, 'f', 0, 64)
		b = append(b, ',')
		b = strconv.AppendFloat(b, s.OutBps, 'f', 0, 64)
		b = append(b, ',')
		b = strconv.AppendFloat(b, s.InPps, 'f', 1, 64)
		b = append(b, ',')
		b = strconv.AppendFloat(b, s.OutPps, 'f', 1, 64)
		b = append(b, '\n')
		if _, err := bw.Write(b); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// csvFileName returns the export file name for an interface: tsnmp_<host>_<interface>_<time>.csv,
// with every character not allowed (or awkward) in file names replaced by "_".
func csvFileName(host, ifName string, t time.Time) string {
	return "tsnmp_" + fileNamePart(host) + "_" + fileNamePart(ifName) + "_" + t.Format("20060102-150405") + ".csv"
}

func fileNamePart(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, s)
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return s
}

// createNew creates name, or name with a "-2", "-3", ... suffix if it exists:
// an export never overwrites a file.
func createNew(name string) (*os.File, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		// #nosec G302 G304 -- a CSV export the user asked for, in the current directory, with a sanitized name
		f, err := os.OpenFile(n, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if !errors.Is(err, fs.ErrExist) || i >= 99 {
			return f, err
		}
	}
}

// exportCSV saves all measurements of an interface into a new CSV file in the
// current directory and returns its absolute path.
func exportCSV(ifc *Iface, host string, now time.Time) (string, error) {
	f, err := createNew(csvFileName(host, ifc.Name, now))
	if err != nil {
		return "", err
	}
	path, _ := filepath.Abs(f.Name())
	if err := writeCSV(f, ifc.Hist); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}

// saveCSV exports the measurements of an interface (w on the graph) and
// shows the result.
func (a *App) saveCSV(ifIndex int) {
	ifc, ok := a.store.Get(ifIndex)
	switch {
	case !ok:
		return
	case ifc.Hist.Len() == 0:
		a.message("No measured data to save yet.")
		return
	}
	host := a.sysName
	if host == "" {
		host = a.host
	}
	path, err := exportCSV(&ifc, host, time.Now())
	if err != nil {
		a.message("Saving the measurements failed:\n" + err.Error())
		return
	}
	a.message(fmt.Sprintf("%d measurements saved to\n%s", ifc.Hist.Len(), path))
}
