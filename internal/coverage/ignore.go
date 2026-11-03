package coverage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
)

type Ignore struct {
	Dirs           *regexp.Regexp
	Files          *regexp.Regexp
	GeneratedFiles bool
	NonCodeLines   bool
	cache          map[string]bool
}

func (i *Ignore) Match(fileName string, data []byte) (ret bool) {
	fileName = NormalizeImportPath(fileName)

	if i.cache == nil {
		i.cache = map[string]bool{}
	} else if match, exists := i.cache[fileName]; exists {
		return match
	}

	dir := path.Dir(fileName)

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
		parent := path.Dir(dir)
		if parent == dir || parent == "." {
			return false
		}
		dir = parent
	}
}
