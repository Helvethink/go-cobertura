package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

type Ignore struct {
	Dirs           *regexp.Regexp
	Files          *regexp.Regexp
	GeneratedFiles bool
	NonCodeLines   bool
	cache          map[string]bool
}

func (i *Ignore) Match(fileName string, data []byte) (ret bool) {
	fileName = strings.ReplaceAll(fileName, "\\", "/")

	if i.cache == nil {
		i.cache = map[string]bool{}
	} else if match, exists := i.cache[fileName]; exists {
		return match
	}

	dir := filepath.Dir(fileName)
	// NOTE(boumenot): I think this is a behavior change or due to where I run the unit tests.  On Windows
	//                 the directory separator is being changed from / to \.  I **think** this is a behavior
	//                 change because this is old code, and only the Go version has been updated.
	dir = strings.ReplaceAll(dir, "\\", "/")

	if i.dirMatch(dir) ||
		(i.Files != nil && i.Files.MatchString(fileName)) {
		ret = true
	} else if i.GeneratedFiles {
		if data == nil {
			return false // no cache if no content provided
		}
		ret = isGenerated(data)
	}

	i.cache[fileName] = ret

	return ret
}

func isGenerated(data []byte) bool {
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"",
		data,
		parser.ParseComments|parser.PackageClauseOnly,
	)
	return err == nil && ast.IsGenerated(file)
}

func (i *Ignore) dirMatch(dir string) bool {
	if i.Dirs == nil {
		return false
	}

	for {
		if i.Dirs.MatchString(dir) {
			return true
		}
		dir, _ = filepath.Split(dir)
		if dir == "" {
			return false
		}
		dir = dir[:len(dir)-1] // without last separator
	}
}
