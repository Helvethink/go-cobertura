// Imported from https://code.google.com/p/go/source/browse/cmd/cover/profile.go?repo=tools&r=c10a9dd5e0b0a859a8385b6f004584cb083a3934

// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Profile represents the profiling data for a specific file.
type Profile struct {
	FileName string
	Mode     string
	Blocks   []ProfileBlock
}

// ProfileBlock represents a single block of profiling data.
type ProfileBlock struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	NumStmt, Count      int
}

type byFileName []*Profile

func (p byFileName) Len() int           { return len(p) }
func (p byFileName) Less(i, j int) bool { return p[i].FileName < p[j].FileName }
func (p byFileName) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// ParseProfiles parses profile data from the given Reader and returns a
// Profile for each file.
func ParseProfiles(in io.Reader, ignore *Ignore) ([]*Profile, error) {
	files := make(map[string]*Profile)
	// First line is "mode: foo", where foo is "set", "count", or "atomic".
	// Rest of file is in the format
	//      encoding/base64/base64.go:34.44,37.40 3 1
	// where the fields are: name.go:line.column,line.column numberOfStatements count
	s := bufio.NewScanner(in)
	mode := ""
	lineNumber := 0
	for s.Scan() {
		lineNumber++
		line := strings.TrimSuffix(s.Text(), "\r")
		if mode == "" {
			const p = "mode: "
			if !strings.HasPrefix(line, p) || line == p {
				return nil, fmt.Errorf("bad mode line: %v", line)
			}
			mode = line[len(p):]
			switch mode {
			case "set", "count", "atomic":
			default:
				return nil, fmt.Errorf("unsupported coverage mode %q", mode)
			}
			continue
		}
		if line == "" {
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("bad profile line %d: %q", lineNumber, line)
		}
		block, err := parseProfileBlock(m)
		if err != nil {
			return nil, fmt.Errorf("bad profile line %d: %w", lineNumber, err)
		}
		fn := m[1]
		if ignore.Match(fn, nil) {
			continue
		}
		p := files[fn]
		if p == nil {
			p = &Profile{
				FileName: fn,
				Mode:     mode,
			}
			files[fn] = p
		}
		p.Blocks = append(p.Blocks, block)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}

	for _, p := range files {
		sort.Sort(blocksByStart(p.Blocks))
	}

	// Generate a sorted slice.
	profiles := make([]*Profile, 0, len(files))
	for _, profile := range files {
		profiles = append(profiles, profile)
	}
	sort.Sort(byFileName(profiles))
	return profiles, nil
}

type blocksByStart []ProfileBlock

func (b blocksByStart) Len() int      { return len(b) }
func (b blocksByStart) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b blocksByStart) Less(i, j int) bool {
	bi, bj := b[i], b[j]
	return bi.StartLine < bj.StartLine || bi.StartLine == bj.StartLine && bi.StartCol < bj.StartCol
}

var lineRe = regexp.MustCompile(`^(.+):([0-9]+)\.([0-9]+),([0-9]+)\.([0-9]+) ([0-9]+) ([0-9]+)$`)

func parseProfileBlock(match []string) (ProfileBlock, error) {
	names := [...]string{"start line", "start column", "end line", "end column", "statement count", "hit count"}
	var values [len(names)]int
	for index, name := range names {
		value := match[index+2]
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return ProfileBlock{}, fmt.Errorf("invalid %s %q: %w", name, value, err)
		}
		values[index] = parsed
	}

	return ProfileBlock{
		StartLine: values[0],
		StartCol:  values[1],
		EndLine:   values[2],
		EndCol:    values[3],
		NumStmt:   values[4],
		Count:     values[5],
	}, nil
}
