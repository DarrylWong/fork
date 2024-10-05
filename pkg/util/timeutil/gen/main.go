// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

// This binary takes the tzdata from Go's source and extracts all
// timezones names from them. From there, a mapping of lowercased
// timezone map to the tzdata names is generated into a file.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
)

const header = `// Code generated by pkg/util/timeutil/gen/main.go - DO NOT EDIT.
//
// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package timeutil

var lowercaseTimezones = map[string]string{
`

func main() {
	var filenameFlag = flag.String("filename", "lowercase_timezones_generated.go", "filename path")
	var zoneInfoFlag = flag.String("zoneinfo", filepath.Join(runtime.GOROOT(), "lib", "time", "zoneinfo.zip"), "path to zoneinfo.zip")
	var crlfmtFlag = flag.String("crlfmt", "crlfmt", "crlfmt binary to use")
	flag.Parse()

	zipdataFile, err := os.Open(*zoneInfoFlag)
	if err != nil {
		log.Fatalf("error loading zoneinfo.zip: %+v\n", err)
	}

	zipdata, err := io.ReadAll(zipdataFile)
	if err != nil {
		log.Fatalf("error reading all content from zoneinfo.zip: %+v\n", err)
	}

	if err := zipdataFile.Close(); err != nil {
		log.Fatalf("error closing zoneinfo.zip: %+v\n", err)
	}

	zones, err := loadFromTZData(string(zipdata))
	if err != nil {
		log.Fatalf("error parsing tzdata: %+v", err)
	}
	sort.Strings(zones)

	of, err := os.Create(*filenameFlag)
	if err != nil {
		log.Fatalf("failed to create file: %+v", err)
	}

	buf := bufio.NewWriter(of)
	_, err = buf.WriteString(header)
	if err != nil {
		log.Fatalf("failed to write header: %+v", err)
	}

	for _, zone := range zones {
		fmt.Fprintf(buf, "\t`%s`: `%s`,\n", strings.ToLower(zone), zone)
	}
	fmt.Fprintf(buf, "}\n")

	if err := buf.Flush(); err != nil {
		log.Fatalf("error flushing buffer: %+v", err)
	}
	if err := of.Close(); err != nil {
		log.Fatalf("error closing file: %+v", err)
	}
	if err := exec.Command(*crlfmtFlag, "-w", "-tab", "2", *filenameFlag).Run(); err != nil {
		log.Fatalf("failed to run crlfmt: %+v", err)
	}
}

// get4s returns the little-endian 32-bit value at the start of s.
func get4s(s string) int {
	if len(s) < 4 {
		return 0
	}
	return int(s[0]) | int(s[1])<<8 | int(s[2])<<16 | int(s[3])<<24
}

// get2s returns the little-endian 16-bit value at the start of s.
func get2s(s string) int {
	if len(s) < 2 {
		return 0
	}
	return int(s[0]) | int(s[1])<<8
}

// loadFromTZData loads all zone names from a tzdata zip.
func loadFromTZData(z string) ([]string, error) {
	const (
		zecheader = 0x06054b50
		zcheader  = 0x02014b50
		ztailsize = 22
	)

	idx := len(z) - ztailsize
	n := get2s(z[idx+10:])
	idx = get4s(z[idx+16:])

	var ret []string
	for i := 0; i < n; i++ {
		// See time.loadTzinfoFromZip for zip entry layout.
		if get4s(z[idx:]) != zcheader {
			break
		}
		meth := get2s(z[idx+10:])
		namelen := get2s(z[idx+28:])
		xlen := get2s(z[idx+30:])
		fclen := get2s(z[idx+32:])
		zname := z[idx+46 : idx+46+namelen]
		idx += 46 + namelen + xlen + fclen
		// Ignore directories.
		if !strings.HasSuffix(zname, "/") {
			ret = append(ret, zname)
		}
		if meth != 0 {
			return nil, errors.Newf("unsupported compression for %s in embedded tzdata", zname)
		}
	}

	return ret, nil
}
