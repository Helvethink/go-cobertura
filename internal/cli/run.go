package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"

	"github.com/Helvethink/go-cobertura/internal/cobertura"
	coverprofile "github.com/Helvethink/go-cobertura/internal/coverage"
)

// Run parses command-line arguments, converts stdin, and returns a process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options := cobertura.Options{}
	strict := false
	flags := flag.NewFlagSet("go-cobertura", flag.ContinueOnError)
	flags.SetOutput(stderr)

	flags.BoolVar(&options.ByFiles, "by-files", false, "code coverage by file, not class")
	flags.BoolVar(&options.Ignore.GeneratedFiles, "ignore-gen-files", false, "ignore generated files")
	flags.BoolVar(&options.Ignore.NonCodeLines, "ignore-non-code-lines", false, "ignore lines with no code")
	flags.BoolVar(&strict, "strict", false, "exit with error if any profiles are skipped")
	flags.StringVar(&options.ProjectDir, "path", "", "set the project directory for package resolution")
	ignoreDirsRe := flags.String("ignore-dirs", "", "ignore dirs matching this regexp")
	ignoreFilesRe := flags.String("ignore-files", "", "ignore files matching this regexp")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	if !compileIgnorePatterns(stderr, &options.Ignore, *ignoreDirsRe, *ignoreFilesRe) {
		return 1
	}

	var err error
	if strict {
		err = cobertura.ConvertStrict(stdin, stdout, options)
	} else {
		err = cobertura.Convert(stdin, stdout, stderr, options)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "code coverage conversion failed: %s\n", err)
		return 1
	}
	return 0
}

func compileIgnorePatterns(stderr io.Writer, ignore *coverprofile.Ignore, dirs, files string) bool {
	var err error
	if dirs != "" {
		ignore.Dirs, err = regexp.Compile(dirs)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Bad -ignore-dirs regexp: %s\n", err)
			return false
		}
	}
	if files != "" {
		ignore.Files, err = regexp.Compile(files)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Bad -ignore-files regexp: %s\n", err)
			return false
		}
	}
	return true
}
