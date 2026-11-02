package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

func TestMainOutput(t *testing.T) {
	if os.Getenv("GO_COBERTURA_TEST_MAIN") == "1" {
		os.Exit(run(nil, os.Stdin, os.Stdout, os.Stderr))
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainOutput$")
	cmd.Env = append(os.Environ(), "GO_COBERTURA_TEST_MAIN=1")
	cmd.Stdin = strings.NewReader("mode: set")
	output, err := cmd.Output()
	require.NoError(t, err)
	require.Contains(t, string(output), xml.Header)
	require.Contains(t, string(output), coberturaDTDDecl)
}

func TestRunDoesNotRetainOptions(t *testing.T) {
	input, err := os.ReadFile("testdata/testdata_set.txt")
	require.NoError(t, err)

	var byFilesOutput bytes.Buffer
	require.Zero(t, run(
		[]string{"-by-files"},
		bytes.NewReader(input),
		&byFilesOutput,
		io.Discard,
	))
	require.Contains(t, byFilesOutput.String(), `name="testdata.func1.go"`)

	var defaultOutput bytes.Buffer
	require.Zero(t, run(nil, bytes.NewReader(input), &defaultOutput, io.Discard))
	require.Contains(t, defaultOutput.String(), `name="-"`)
	require.NotContains(t, defaultOutput.String(), `name="testdata.func1.go"`)
}

func TestRunRejectsInvalidIgnorePattern(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		[]string{"-ignore-files", "["},
		strings.NewReader("mode: set"),
		io.Discard,
		&stderr,
	)

	require.Equal(t, 1, exitCode)
	require.Contains(t, stderr.String(), "Bad -ignore-files regexp")
}

func TestRunHelpSucceeds(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run([]string{"-help"}, strings.NewReader("mode: set"), io.Discard, &stderr)

	require.Zero(t, exitCode)
	require.Contains(t, stderr.String(), "Usage of go-cobertura")
}

func TestRunAcceptsEmptyInput(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(nil, strings.NewReader(""), &stdout, &stderr)

	require.Zero(t, exitCode)
	require.Contains(t, stdout.String(), "<coverage")
	require.Empty(t, stderr.String())
}

func TestRunRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		input      string
		wantStderr string
	}{
		{
			name:       "unknown flag",
			args:       []string{"-unknown"},
			input:      "mode: set",
			wantStderr: "flag provided but not defined",
		},
		{
			name:       "malformed coverage",
			input:      "invalid data",
			wantStderr: "code coverage conversion failed: bad mode line",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := run(test.args, strings.NewReader(test.input), &stdout, &stderr)

			require.Equal(t, 1, exitCode)
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), test.wantStderr)
		})
	}
}

func TestRunReportsOutputWriterFailure(t *testing.T) {
	stdout := &failOnceWriter{shouldFail: func([]byte) bool { return true }}
	var stderr bytes.Buffer

	exitCode := run(nil, strings.NewReader("mode: set"), stdout, &stderr)

	require.Equal(t, 1, exitCode)
	require.True(t, stdout.failed)
	require.Contains(t, stderr.String(), "code coverage conversion failed: writing xml header")
	require.Contains(t, stderr.String(), errTestWrite.Error())
}

func TestConvertParseProfilesError(t *testing.T) {
	var output bytes.Buffer
	err := convert(strings.NewReader("invalid data"), &output, io.Discard, conversionOptions{})
	require.Error(t, err)
	require.Equal(t, "bad mode line: invalid data", err.Error())
}

func convertFixture(t *testing.T, filename string, ignore *Ignore) Coverage {
	t.Helper()
	return convertFixtureWithOptions(t, filename, conversionOptions{ignore: *ignore})
}

func convertFixtureWithOptions(t *testing.T, filename string, options conversionOptions) Coverage {
	t.Helper()

	data, err := os.ReadFile(filename)
	require.NoError(t, err)
	return convertAndDecode(t, bytes.NewReader(data), options)
}

func convertAndDecode(t *testing.T, input io.Reader, options conversionOptions) Coverage {
	t.Helper()

	var output bytes.Buffer
	require.NoError(t, convert(input, &output, io.Discard, options))

	var coverage Coverage
	require.NoError(t, xml.NewDecoder(&output).Decode(&coverage))
	return coverage
}

var errTestWrite = errors.New("test write failure")

type failOnceWriter struct {
	shouldFail func([]byte) bool
	failed     bool
}

func (w *failOnceWriter) Write(data []byte) (int, error) {
	if !w.failed && w.shouldFail(data) {
		w.failed = true
		return 0, errTestWrite
	}
	return len(data), nil
}

func TestConvertOutputError(t *testing.T) {
	tests := []struct {
		name       string
		shouldFail func([]byte) bool
		context    string
	}{
		{
			name: "XML header",
			shouldFail: func(data []byte) bool {
				return bytes.Contains(data, []byte(xml.Header))
			},
			context: "writing xml header",
		},
		{
			name: "Cobertura DTD",
			shouldFail: func(data []byte) bool {
				return bytes.Contains(data, []byte(coberturaDTDDecl))
			},
			context: "writing cobertura dtd",
		},
		{
			name: "coverage XML",
			shouldFail: func(data []byte) bool {
				return bytes.Contains(data, []byte("<coverage"))
			},
			context: "encoding coverage xml",
		},
		{
			name: "trailing newline",
			shouldFail: func(data []byte) bool {
				return bytes.Equal(data, []byte("\n"))
			},
			context: "writing trailing newline",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &failOnceWriter{shouldFail: test.shouldFail}
			err := convert(strings.NewReader("mode: set"), writer, io.Discard, conversionOptions{})

			require.True(t, writer.failed, "test writer did not reach the expected output stage")
			require.ErrorIs(t, err, errTestWrite)
			require.ErrorContains(t, err, test.context)
		})
	}
}

func TestConvertEmpty(t *testing.T) {
	data := `mode: set`

	var output bytes.Buffer
	err := convert(strings.NewReader(data), &output, io.Discard, conversionOptions{})
	require.NoError(t, err)
	require.NotContains(t, output.String(), "NaN")
	require.NotContains(t, output.String(), "Inf")

	v := Coverage{}
	dec := xml.NewDecoder(strings.NewReader(output.String()))
	err = dec.Decode(&v)
	require.NoError(t, err)

	require.Equal(t, "coverage", v.XMLName.Local)
	require.Nil(t, v.Sources)
	require.Nil(t, v.Packages)
}

func TestParseProfileNilPackages(t *testing.T) {
	v := Coverage{}
	profile := Profile{FileName: "does-not-exist"}
	_, err := v.parseProfile(&profile, nil, conversionOptions{})
	require.NoError(t, err)
}

func TestParseProfileEmptyPackages(t *testing.T) {
	v := Coverage{}
	profile := Profile{FileName: "does-not-exist"}
	_, err := v.parseProfile(&profile, &packages.Package{}, conversionOptions{})
	require.NoError(t, err)
}

func TestParseProfileDoesNotExist(t *testing.T) {
	v := Coverage{}
	profile := Profile{FileName: "example.com/project/does-not-exist.go"}

	pkg := packages.Package{
		Name: "project",
		Module: &packages.Module{
			Path: "example.com/project",
		},
	}

	_, err := v.parseProfile(&profile, &pkg, conversionOptions{})
	require.Error(t, err)

	if !strings.Contains(err.Error(), "unable to determine file path") {
		t.Error(err.Error())
	}
}

func TestParseProfileModulePaths(t *testing.T) {
	tests := []struct {
		name         string
		profileName  string
		modulePath   string
		wantPackage  string
		wantFilename string
	}{
		{
			name:         "module root",
			profileName:  "example.com/project/func1.go",
			modulePath:   "example.com/project",
			wantPackage:  "example.com/project",
			wantFilename: "func1.go",
		},
		{
			name:         "nested package",
			profileName:  "example.com/project/testdata/func1.go",
			modulePath:   "example.com/project",
			wantPackage:  "example.com/project/testdata",
			wantFilename: "testdata/func1.go",
		},
		{
			name:         "Windows separators",
			profileName:  `example.com\project\testdata\func1.go`,
			modulePath:   "example.com/project",
			wantPackage:  "example.com/project/testdata",
			wantFilename: "testdata/func1.go",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coverage := Coverage{}
			profile := Profile{FileName: test.profileName}
			pkg := packages.Package{
				ID:      getPackageName(test.profileName),
				GoFiles: []string{"testdata/func1.go"},
				Module: &packages.Module{
					Path: test.modulePath,
				},
			}

			skipped, err := coverage.parseProfile(&profile, &pkg, conversionOptions{})
			require.NoError(t, err)
			require.False(t, skipped)
			require.Len(t, coverage.Packages, 1)
			require.Equal(t, test.wantPackage, coverage.Packages[0].Name)
			require.Len(t, coverage.Packages[0].Classes, 1)
			require.Equal(t, test.wantFilename, coverage.Packages[0].Classes[0].Filename)
		})
	}
}

func TestGetPackageNameUsesImportPathSeparators(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{name: "slash separated", filename: "example.com/project/pkg/file.go", want: "example.com/project/pkg"},
		{name: "Windows separated", filename: `example.com\project\pkg\file.go`, want: "example.com/project/pkg"},
		{name: "module root file", filename: "file.go", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, getPackageName(test.filename))
		})
	}
}

func TestParseProfileRejectsMismatchedModulePath(t *testing.T) {
	coverage := Coverage{}
	profile := Profile{FileName: "example.com/other/file.go"}
	pkg := packages.Package{
		GoFiles: []string{"testdata/func1.go"},
		Module: &packages.Module{
			Path: "example.com/project",
		},
	}

	require.NotPanics(t, func() {
		_, err := coverage.parseProfile(&profile, &pkg, conversionOptions{})
		require.ErrorContains(t, err, `profile "example.com/other/file.go" is outside module "example.com/project"`)
	})
}

func TestParseProfileRejectsEmptyModulePath(t *testing.T) {
	coverage := Coverage{}
	profile := Profile{FileName: "example.com/project/file.go"}
	pkg := packages.Package{Module: &packages.Module{}}

	_, err := coverage.parseProfile(&profile, &pkg, conversionOptions{})
	require.ErrorContains(t, err, "module path is empty")
}

func TestParseProfileLoadsBuildConstrainedFile(t *testing.T) {
	moduleDir := t.TempDir()
	sourceDir := filepath.Join(moduleDir, "src")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sourceDir, "result_x86.go"),
		[]byte("package src\n\nfunc result() {}\n"),
		0o644,
	))

	coverage := Coverage{}
	profile := Profile{
		FileName: "example.com/project/src/result_x86.go",
		Mode:     "set",
		Blocks: []ProfileBlock{{
			StartLine: 3,
			StartCol:  1,
			EndLine:   3,
			EndCol:    17,
			NumStmt:   1,
			Count:     1,
		}},
	}
	pkg := packages.Package{
		ID: "example.com/project/src",
		Module: &packages.Module{
			Path: "example.com/project",
			Dir:  moduleDir,
		},
	}

	skipped, err := coverage.parseProfile(&profile, &pkg, conversionOptions{})
	require.NoError(t, err)
	require.False(t, skipped)
	require.Len(t, coverage.Packages, 1)
	require.Len(t, coverage.Packages[0].Classes, 1)
}

func TestParseProfileRejectsPathTraversal(t *testing.T) {
	coverage := Coverage{}
	profile := Profile{FileName: "example.com/project/../secret.go"}
	pkg := packages.Package{
		Module: &packages.Module{
			Path: "example.com/project",
			Dir:  t.TempDir(),
		},
	}

	_, err := coverage.parseProfile(&profile, &pkg, conversionOptions{})
	require.ErrorContains(t, err, `source path "../secret.go" is outside module directory`)
}

func TestParseProfileNotReadable(t *testing.T) {
	v := Coverage{}
	profile := Profile{FileName: os.DevNull}
	_, err := v.parseProfile(&profile, nil, conversionOptions{})
	require.NoError(t, err)
}

func TestParseProfilePermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod is not supported by Windows")
	}

	tempFile, err := os.CreateTemp("", "not-readable")
	require.NoError(t, err)

	defer func() { err := os.Remove(tempFile.Name()); require.NoError(t, err) }()
	err = tempFile.Chmod(0o00)
	require.NoError(t, err)
	v := Coverage{}
	profile := Profile{FileName: tempFile.Name()}
	pkg := packages.Package{
		GoFiles: []string{
			tempFile.Name(),
		},
		Module: &packages.Module{
			Path: filepath.Dir(tempFile.Name()),
		},
	}
	_, err = v.parseProfile(&profile, &pkg, conversionOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

// TestConvertNilModule verifies that convert does not panic when
// packages.Load returns a package with a nil Module field. This can happen
// when:
//   - The profile references a standard library package (e.g. "fmt"),
//     which is not part of any Go module.
//   - The code is built in GOPATH mode without module support.
//   - A dependency fails to fully resolve (e.g. private repo auth
//     failures, missing transitive dependencies), causing packages.Load
//     to return a partially populated Package with Module == nil.
func TestConvertNilModule(t *testing.T) {
	// A coverage profile referencing a stdlib package triggers the nil
	// Module path because standard library packages have no module info.
	data := "mode: set\nfmt/print.go:1.1,1.1 1 1\n"

	var output bytes.Buffer
	require.NotPanics(t, func() {
		require.NoError(t, convert(strings.NewReader(data), &output, io.Discard, conversionOptions{}))
	})
}

func TestConvertSetMode(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_set.txt", &Ignore{
		GeneratedFiles: true,
		Files:          regexp.MustCompile(`[\\/]func[45]\.go$`),
	})

	require.Equal(t, "coverage", v.XMLName.Local)
	require.Len(t, v.Sources, 1)
	require.Len(t, v.Packages, 1)

	p := v.Packages[0]
	require.Equal(t, "github.com/Helvethink/go-cobertura/testdata", strings.TrimRight(p.Name, "/"))
	require.NotNil(t, p.Classes)
	require.Len(t, p.Classes, 2)

	c := p.Classes[0]
	require.Equal(t, "-", c.Name)
	require.Equal(t, "testdata/func1.go", c.Filename)
	require.NotNil(t, c.Methods)
	require.Len(t, c.Methods, 1)
	require.NotNil(t, c.Lines)
	require.Len(t, c.Lines, 4)

	m := c.Methods[0]
	require.Equal(t, "Func1", m.Name)
	require.NotNil(t, c.Lines)
	require.Len(t, c.Lines, 4)

	var l *Line
	if l = m.Lines[0]; l.Number != 4 || l.Hits != 1 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}
	// Line 5 is partially covered (coverage up to column 16), but Cobertura doesn't support partial hits, so it should be counted as covered.
	if l = m.Lines[1]; l.Number != 5 || l.Hits != 1 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}
	if l = m.Lines[2]; l.Number != 6 || l.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}
	if l = m.Lines[3]; l.Number != 7 || l.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}

	if l = c.Lines[0]; l.Number != 4 || l.Hits != 1 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}
	if l = c.Lines[1]; l.Number != 5 || l.Hits != 1 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}
	if l = c.Lines[2]; l.Number != 6 || l.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}
	if l = c.Lines[3]; l.Number != 7 || l.Hits != 0 {
		t.Errorf("unmatched line: Number:%d, Hits:%d", l.Number, l.Hits)
	}

	c = p.Classes[1]
	require.Equal(t, "Type1", c.Name)
	require.Equal(t, "testdata/func2.go", c.Filename)
	require.NotNil(t, c.Methods)
	require.Len(t, c.Methods, 3)
}

// TestConvertByFilesMode verifies that the -by-files flag causes class
// names to be derived from the file path (dot-separated) instead of
// the receiver type name.
func TestConvertByFilesMode(t *testing.T) {
	v := convertFixtureWithOptions(t, "testdata/testdata_set.txt", conversionOptions{
		byFiles: true,
		ignore: Ignore{
			GeneratedFiles: true,
			Files:          regexp.MustCompile(`[\\/]func[45]\.go$`),
		},
	})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]
	require.NotNil(t, p.Classes)

	// With -by-files, each file gets its own class with a dot-separated
	// path name instead of receiver type names like "-" or "Type1".
	classNames := make(map[string]bool)
	for _, c := range p.Classes {
		classNames[c.Name] = true
	}

	// Should NOT contain receiver-based names.
	require.NotContains(t, classNames, "-",
		"class name should not be '-' in by-files mode")
	require.NotContains(t, classNames, "Type1",
		"class name should not be receiver type in by-files mode")

	// Should contain dot-separated file paths.
	require.Contains(t, classNames, "testdata.func1.go",
		"class name should be dot-separated file path")
	require.Contains(t, classNames, "testdata.func2.go",
		"class name should be dot-separated file path")
}

// TestConvertInitFunc verifies that init() functions are included in
// coverage output. init() is a regular FuncDecl with no receiver.
func TestConvertInitFunc(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_init.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]
	require.NotNil(t, p.Classes)

	var methods []string
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_init.go" {
			for _, m := range c.Methods {
				methods = append(methods, m.Name)
			}
		}
	}

	require.Contains(t, methods, "init",
		"init() function should be included in coverage output")
}

// TestConvertClosureArg verifies that anonymous functions passed as
// arguments (e.g. sort.Slice callback) are covered by the enclosing
// FuncDecl. Since Visit returns nil after processing a FuncDecl, the
// closure is NOT visited separately — its lines are counted as part
// of the parent function's line range.
func TestConvertClosureArg(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_closure_arg.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]

	// Should have exactly one class with one method.
	var totalMethods int
	var totalLines int
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_closure_arg.go" {
			for _, m := range c.Methods {
				totalMethods++
				totalLines += len(m.Lines)
			}
		}
	}

	require.Equal(t, 1, totalMethods,
		"closure arg should not create a separate method")
	require.Positive(t, totalLines,
		"closure lines should be covered by enclosing FuncDecl")
}

// TestConvertReceiverTypes verifies that class names are correctly
// derived from various receiver types: value receivers, pointer
// receivers (star trimmed), and different type names.
func TestConvertReceiverTypes(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_receivers.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]

	// Build map of class name -> method names.
	classMap := make(map[string][]string)
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_receivers.go" {
			for _, m := range c.Methods {
				classMap[c.Name] = append(classMap[c.Name], m.Name)
			}
		}
	}

	// Value receiver: MyService -> class "MyService"
	require.Contains(t, classMap, "MyService",
		"value receiver should produce class named after type")
	require.Contains(t, classMap["MyService"], "Handle")

	// Pointer receiver: *MyService -> class "MyService" (star trimmed)
	require.Contains(t, classMap["MyService"], "Process",
		"pointer receiver should map to same class as value receiver")

	// Different type: *Wrapper -> class "Wrapper"
	require.Contains(t, classMap, "Wrapper")
	require.Contains(t, classMap["Wrapper"], "Run")
}

// TestConvertMultipleFiles verifies that a coverage profile spanning
// multiple files within one package produces the correct class/method
// structure with methods grouped by their respective files and types.
func TestConvertMultipleFiles(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_multi_file.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]

	// Collect filenames and class names.
	filenames := make(map[string]bool)
	classNames := make(map[string]bool)
	for _, c := range p.Classes {
		filenames[c.Filename] = true
		classNames[c.Name] = true
	}

	// Should have classes from multiple files.
	require.Contains(t, filenames, "testdata/func1.go")
	require.Contains(t, filenames, "testdata/func_init.go")
	require.Contains(t, filenames, "testdata/func_receivers.go")

	// Should have both package-level ("-") and receiver-based classes.
	require.Contains(t, classNames, "-",
		"package-level functions should have class '-'")
	require.Contains(t, classNames, "MyService",
		"receiver methods should have type-based class")

	// Lines should sum correctly across all classes.
	require.Positive(t, v.LinesValid)
	require.Positive(t, v.LinesCovered)
}

// TestConvertCountMode verifies that in "count" mode, hit counts are
// summed (not OR'd) when the same line appears in multiple blocks.
func TestConvertCountMode(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_count.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]
	require.Len(t, p.Classes, 1)
	c := p.Classes[0]
	require.Len(t, c.Methods, 1)
	m := c.Methods[0]

	// Line 4 appears in two blocks with counts 3 and 2 — should sum to 5.
	var line4 *Line
	var line5 *Line
	for _, l := range m.Lines {
		switch l.Number {
		case 4:
			line4 = l
		case 5:
			line5 = l
		}
	}

	require.NotNil(t, line4, "line 4 should exist")
	require.Equal(t, int64(5), line4.Hits,
		"count mode should sum hits (3+2=5)")

	require.NotNil(t, line5, "line 5 should exist")
	require.Equal(t, int64(6), line5.Hits,
		"count mode should sum hits across all overlapping blocks")
}

// TestConvertOverlappingBlocks verifies deduplication of coverage blocks
// that overlap on line boundaries. This occurs with -coverpkg, where go
// test produces separate blocks from each test package that may cover
// different spans of the same lines.
//
// The test profile has overlapping blocks for func_overlap.go:
//
//	Block A: 9.30,10.13 (hit)   — covers lines 9-10
//	Block B: 10.13,12.3 (miss)  — covers lines 10-12
//	Block C: 12.3,13.2  (hit)   — covers line 12-13
//	Block D: 9.30,12.3  (hit)   — covers lines 9-12 (same start as A, different end)
//	Block E: 12.3,13.2  (miss)  — covers line 12-13
//
// BUG: The old AddOrUpdateLine only checks the *last* added line for
// deduplication. When overlapping blocks produce non-adjacent duplicate
// line entries, they are added as separate entries instead of merged.
// This results in 10 line entries instead of 5, with some lines showing
// hits=0 even though another entry for the same line has hits=1.
//
// See: https://github.com/boumenot/gocover-cobertura/pull/24
func TestConvertOverlappingBlocks(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_overlap.txt", &Ignore{})

	require.Equal(t, "coverage", v.XMLName.Local)
	require.Len(t, v.Packages, 1)

	p := v.Packages[0]
	require.NotNil(t, p.Classes)
	require.Len(t, p.Classes, 1)

	c := p.Classes[0]
	require.Equal(t, "-", c.Name)
	require.Equal(t, "testdata/func_overlap.go", c.Filename)
	require.Len(t, c.Methods, 1)

	m := c.Methods[0]
	require.Equal(t, "FuncOverlap", m.Name)

	// With PR #24's fix, overlapping blocks are properly deduplicated.
	// All 5 lines should be unique and covered (OR of overlapping blocks).
	require.Len(t, m.Lines, 5,
		"expected 5 unique lines after dedup fix")

	for _, l := range m.Lines {
		require.Equal(t, int64(1), l.Hits,
			"line %d should be covered (OR of overlapping blocks)", l.Number)
	}
}

// TestConvertGlobalVarFunc verifies that package-level variable functions
// (e.g. `var Foo = func() { ... }`) have their coverage lines included.
// Previously these were dropped because the AST visitor only matched
// *ast.FuncDecl, and variable functions are *ast.ValueSpec.
//
// See: https://github.com/boumenot/gocover-cobertura/pull/25
func TestConvertGlobalVarFunc(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_varfunc.txt", &Ignore{})

	require.Equal(t, "coverage", v.XMLName.Local)
	require.Len(t, v.Packages, 1)

	p := v.Packages[0]
	require.NotNil(t, p.Classes)

	// Collect methods from func_varfunc.go across all classes.
	var methods []string
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_varfunc.go" {
			for _, m := range c.Methods {
				methods = append(methods, m.Name)
			}
		}
	}

	// GlobalVarFunc should be included after the fix.
	require.Contains(t, methods, "GlobalVarFunc",
		"GlobalVarFunc should be present in output")
	require.Contains(t, methods, "FuncWithLocalVarFunc",
		"Regular FuncDecl should be present")
}

// TestConvertLocalVarFuncDoubleCounting verifies that local variable
// functions declared with `var` inside a FuncDecl do not cause lines
// to be double-counted. The visitor matches both the enclosing FuncDecl
// (counting all body lines) and the inner ValueSpec, which could inflate
// line counts.
//
// See review comment on PR #25:
// "a local func value will be double counted after this PR"
func TestConvertLocalVarFuncDoubleCounting(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_varfunc.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]

	// Count all lines attributed to func_varfunc.go across all classes.
	var allLines []*Line
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_varfunc.go" {
			allLines = append(allLines, c.Lines...)
		}
	}

	// Check for duplicate line numbers which indicate double-counting.
	lineNumbers := make(map[int]int)
	for _, l := range allLines {
		lineNumbers[l.Number]++
	}

	var duplicates []int
	for lineNum, count := range lineNumbers {
		if count > 1 {
			duplicates = append(duplicates, lineNum)
		}
	}

	// No line should appear more than once. If local var closures are
	// double-counted, this assertion will catch it.
	require.Empty(t, duplicates,
		"lines should not be double-counted; duplicates at: %v", duplicates)
}

// TestConvertNonFuncVarIgnored verifies that non-function package-level
// variables (e.g. `var x = "hello"`) do not produce method entries.
func TestConvertNonFuncVarIgnored(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_varfunc.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]

	var methods []string
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_varfunc.go" {
			for _, m := range c.Methods {
				methods = append(methods, m.Name)
			}
		}
	}

	require.NotContains(t, methods, "NonFuncVar",
		"non-function var should not appear as a method")
}

// TestConvertShortAssignNotDoubleCounted verifies that closures assigned
// with := (AssignStmt) inside a FuncDecl are not double-counted.
func TestConvertShortAssignNotDoubleCounted(t *testing.T) {
	v := convertFixture(t, "testdata/testdata_varfunc.txt", &Ignore{})

	require.Len(t, v.Packages, 1)
	p := v.Packages[0]

	// Find lines for FuncWithShortAssignClosure.
	var allLines []*Line
	for _, c := range p.Classes {
		if c.Filename == "testdata/func_varfunc.go" {
			for _, m := range c.Methods {
				if m.Name == "FuncWithShortAssignClosure" {
					allLines = append(allLines, m.Lines...)
				}
			}
		}
	}

	lineNumbers := make(map[int]int)
	for _, l := range allLines {
		lineNumbers[l.Number]++
	}

	var duplicates []int
	for lineNum, count := range lineNumbers {
		if count > 1 {
			duplicates = append(duplicates, lineNum)
		}
	}

	require.Empty(t, duplicates,
		":= closure lines should not be double-counted; duplicates at: %v", duplicates)
}

// TestConvertWithProjectDir verifies that the -path flag correctly sets the
// working directory for package resolution via packages.Config.Dir, without
// using os.Chdir.
//
// Inspired by: https://github.com/boumenot/gocover-cobertura/pull/23
func TestConvertWithProjectDir(t *testing.T) {
	// Get the absolute path to the repo root.
	absRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	// Read testdata from the absolute path.
	data, err := os.ReadFile(filepath.Join(absRoot, "testdata", "testdata_set.txt"))
	require.NoError(t, err)

	// With an unrelated projectDir, convert produces empty output because
	// packages.Load cannot resolve the testdata packages from there.
	var bufEmpty bytes.Buffer
	err = convert(bytes.NewReader(data), &bufEmpty, io.Discard, conversionOptions{projectDir: t.TempDir()})
	require.NoError(t, err)
	require.NotContains(t, bufEmpty.String(), "line-rate=\"0.1",
		"with an unrelated -path, coverage data should be missing from output")

	// With projectDir pointing to the repo root, convert should produce actual
	// coverage data without changing the process working directory.
	var buf bytes.Buffer
	err = convert(bytes.NewReader(data), &buf, io.Discard, conversionOptions{projectDir: absRoot})
	require.NoError(t, err)
	require.Contains(t, buf.String(), "line-rate=\"0.1",
		"with -path, coverage data should be present in output")
}

// TestConvertStrictModeReturnsErrorOnSkippedProfiles verifies that when
// strict mode is enabled, convert returns an error if any profiles are
// skipped due to missing package/module information.
func TestConvertStrictModeReturnsErrorOnSkippedProfiles(t *testing.T) {
	// A coverage profile referencing a stdlib package triggers the nil
	// Module path — these profiles get skipped with a warning.
	data := "mode: set\nfmt/print.go:1.1,1.1 1 1\n"

	var buf bytes.Buffer
	err := convert(strings.NewReader(data), &buf, io.Discard, conversionOptions{})

	// Without strict mode, convert should succeed (profiles silently skipped).
	require.NoError(t, err, "non-strict mode should not error on skipped profiles")

	// With strict mode, convert should return an error.
	var buf2 bytes.Buffer
	err = convertStrict(strings.NewReader(data), &buf2, conversionOptions{})
	require.Error(t, err, "strict mode should error when profiles are skipped")
	require.Contains(t, err.Error(), "skipped",
		"error message should indicate profiles were skipped")
	require.Empty(t, buf2.String(), "strict validation failure must not publish a partial report")
}

// TestConvertStrictModePassesWhenNoSkips verifies that strict mode does
// not error when all profiles are resolved successfully.
func TestConvertStrictModePassesWhenNoSkips(t *testing.T) {
	pipe1rd, err := os.Open("testdata/testdata_set.txt")
	require.NoError(t, err)

	var buf bytes.Buffer
	err = convertStrict(pipe1rd, &buf, conversionOptions{})
	require.NoError(t, err, "strict mode should pass when all profiles resolve")
	require.Contains(t, buf.String(), "coverage")
}

func TestConvertStrictModePropagatesDestinationError(t *testing.T) {
	writer := &failOnceWriter{shouldFail: func([]byte) bool { return true }}

	err := convertStrict(strings.NewReader("mode: set"), writer, conversionOptions{})

	require.True(t, writer.failed, "strict conversion did not write the validated report")
	require.ErrorIs(t, err, errTestWrite)
	require.ErrorContains(t, err, "writing strict coverage report")
}

func TestConvertSurfacesPackageLoadDiagnostics(t *testing.T) {
	tests := []struct {
		name             string
		goMod            string
		sourcePath       string
		source           string
		profile          string
		packageName      string
		wantWarning      string
		wantCoverage     bool
		wantStrictDetail string
	}{
		{
			name:       "unresolved import keeps usable profile",
			goMod:      "module example.com/diagnostic\n\ngo 1.25.0\n",
			sourcePath: "diagnostic.go",
			source: `package diagnostic

import _ "example.com/missing"

func Covered() {}
`,
			profile:          "example.com/diagnostic/diagnostic.go:5.1,5.18 1 1\n",
			packageName:      "example.com/diagnostic",
			wantWarning:      "no required module provides package example.com/missing",
			wantCoverage:     true,
			wantStrictDetail: `package "example.com/diagnostic"`,
		},
		{
			name: "incomplete private dependency",
			goMod: `module example.com/application

go 1.25.0

require private.example.com/team/dependency v0.0.0

replace private.example.com/team/dependency => ./missing-private-dependency
`,
			profile:          "private.example.com/team/dependency/dependency.go:1.1,1.2 1 1\n",
			packageName:      "private.example.com/team/dependency",
			wantWarning:      "replacement directory ./missing-private-dependency does not exist",
			wantCoverage:     false,
			wantStrictDetail: `package "private.example.com/team/dependency"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(test.goMod), 0o600))
			if test.sourcePath != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, test.sourcePath), []byte(test.source), 0o600))
			}
			input := "mode: set\n" + test.profile

			var output bytes.Buffer
			var warnings bytes.Buffer
			options := conversionOptions{projectDir: dir}
			err := convert(strings.NewReader(input), &output, &warnings, options)
			require.NoError(t, err)
			require.Contains(t, warnings.String(), test.wantWarning)
			require.Contains(t, warnings.String(), test.packageName)
			if test.wantCoverage {
				require.Contains(t, output.String(), "diagnostic.go")
			} else {
				require.NotContains(t, output.String(), "dependency.go")
				require.Contains(t, warnings.String(), "skipping profile")
			}

			output.Reset()
			err = convertStrict(strings.NewReader(input), &output, options)
			require.Error(t, err)
			require.Contains(t, err.Error(), test.wantStrictDetail)
			require.Contains(t, err.Error(), test.wantWarning)
			require.Empty(t, output.String(), "strict validation failure must not publish a partial report")
		})
	}
}

// TestIgnoreNonCodeLines verifies that non code lines are/are not covered, as expected.
func TestIgnoreNonCodeLines(t *testing.T) {
	tests := []struct {
		name          string
		ignoreNonCode bool
		filename      string
		totalLines    int
		coveredLines  []int
	}{
		{
			name:          "if body covered, include non-code lines",
			ignoreNonCode: false,
			filename:      "testdata/testdata_non_code_if_taken.txt",
			totalLines:    10,
			coveredLines:  []int{5, 6, 7, 8, 9, 10, 11, 17, 18, 19},
		},
		{
			name:          "if body covered, ignore non-code lines",
			ignoreNonCode: true,
			filename:      "testdata/testdata_non_code_if_taken.txt",
			totalLines:    5,
			coveredLines:  []int{5, 7, 10, 17, 19},
		},
		{
			name:          "if body not covered, include non-code lines",
			ignoreNonCode: false,
			filename:      "testdata/testdata_non_code_if_not_taken.txt",
			totalLines:    10,
			coveredLines:  []int{5, 6, 7, 17, 18, 19},
		},
		{
			name:          "if body not covered, ignore non-code lines",
			ignoreNonCode: true,
			filename:      "testdata/testdata_non_code_if_not_taken.txt",
			totalLines:    5,
			coveredLines:  []int{5, 7, 17, 19},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				v := convertFixture(t, test.filename, &Ignore{NonCodeLines: test.ignoreNonCode})

				require.Equal(t, "coverage", v.XMLName.Local)
				require.Len(t, v.Packages, 1)

				p := v.Packages[0]
				require.Equal(t, "github.com/Helvethink/go-cobertura/testdata", strings.TrimRight(p.Name, "/"))
				require.NotNil(t, p.Classes)
				require.Len(t, p.Classes, 1)

				c := p.Classes[0]
				require.Equal(t, "-", c.Name)
				require.Equal(t, "testdata/func_noncodelines.go", c.Filename)
				require.NotNil(t, c.Methods)
				require.Len(t, c.Methods, 1)
				require.NotNil(t, c.Lines)
				require.Len(t, c.Lines, test.totalLines)

				hitLines := []int{}
				for _, line := range c.Lines {
					if line.Hits > 0 {
						hitLines = append(hitLines, line.Number)
					}
				}
				require.Len(t, hitLines, len(test.coveredLines))
				require.Equal(t, test.coveredLines, hitLines)
			},
		)
	}
}
