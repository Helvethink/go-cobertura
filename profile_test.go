package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProfilesValidInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fileName string
		mode     string
	}{
		{
			name:     "set mode",
			input:    "mode: set\nexample.com/project/file.go:1.2,3.4 1 5\n",
			fileName: "example.com/project/file.go",
			mode:     "set",
		},
		{
			name:     "count mode with blank line",
			input:    "mode: count\n\nexample.com/project/file.go:1.2,3.4 1 5\n",
			fileName: "example.com/project/file.go",
			mode:     "count",
		},
		{
			name:     "atomic mode with CRLF",
			input:    "mode: atomic\r\nexample.com/project/file.go:1.2,3.4 1 5\r\n",
			fileName: "example.com/project/file.go",
			mode:     "atomic",
		},
		{
			name:     "Windows path",
			input:    "mode: set\nC:\\project\\file.go:1.2,3.4 1 5\n",
			fileName: "C:/project/file.go",
			mode:     "set",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profiles, err := ParseProfiles(strings.NewReader(test.input), &Ignore{})
			require.NoError(t, err)
			require.Len(t, profiles, 1)
			require.Equal(t, test.fileName, profiles[0].FileName)
			require.Equal(t, test.mode, profiles[0].Mode)
			require.Equal(t, []ProfileBlock{{
				StartLine: 1,
				StartCol:  2,
				EndLine:   3,
				EndCol:    4,
				NumStmt:   1,
				Count:     5,
			}}, profiles[0].Blocks)
		})
	}
}

func TestParseProfilesInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		errorText string
	}{
		{
			name:      "unsupported mode",
			input:     "mode: sample\n",
			errorText: `unsupported coverage mode "sample"`,
		},
		{
			name:      "invalid separators",
			input:     "mode: set\nexample.com/project/file.go:1x2,3y4 1 5\n",
			errorText: "bad profile line 2",
		},
		{
			name:      "truncated record",
			input:     "mode: set\nexample.com/project/file.go:1.2,3.4 1\n",
			errorText: "bad profile line 2",
		},
		{
			name:      "overflow",
			input:     "mode: set\nexample.com/project/file.go:999999999999999999999999.2,3.4 1 5\n",
			errorText: "invalid start line",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profiles, err := ParseProfiles(strings.NewReader(test.input), &Ignore{})
			require.ErrorContains(t, err, test.errorText)
			require.Nil(t, profiles)
		})
	}
}

func TestParseProfilesEmptyInput(t *testing.T) {
	profiles, err := ParseProfiles(strings.NewReader(""), &Ignore{})

	require.NoError(t, err)
	require.Empty(t, profiles)
}

func TestParseProfilesRejectsOverflowInNumericFields(t *testing.T) {
	const overflow = "999999999999999999999999"
	tests := []struct {
		name      string
		profile   string
		errorText string
	}{
		{name: "start line", profile: overflow + ".2,3.4 1 5", errorText: "invalid start line"},
		{name: "start column", profile: "1." + overflow + ",3.4 1 5", errorText: "invalid start column"},
		{name: "end line", profile: "1.2," + overflow + ".4 1 5", errorText: "invalid end line"},
		{name: "end column", profile: "1.2,3." + overflow + " 1 5", errorText: "invalid end column"},
		{name: "statement count", profile: "1.2,3.4 " + overflow + " 5", errorText: "invalid statement count"},
		{name: "hit count", profile: "1.2,3.4 1 " + overflow, errorText: "invalid hit count"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := "mode: count\nexample.com/project/file.go:" + test.profile + "\n"
			profiles, err := ParseProfiles(strings.NewReader(input), &Ignore{})

			require.ErrorContains(t, err, test.errorText)
			require.Nil(t, profiles)
		})
	}
}

func TestParseProfilesRejectsLongLine(t *testing.T) {
	input := "mode: set\n" + strings.Repeat("x", 128*1024) + "\n"

	profiles, err := ParseProfiles(strings.NewReader(input), &Ignore{})

	require.Error(t, err)
	require.Nil(t, profiles)
}

func TestParseProfilesPreservesDuplicateBlocks(t *testing.T) {
	const input = `mode: count
example.com/project/file.go:1.2,3.4 1 5
example.com/project/file.go:1.2,3.4 1 7
`

	profiles, err := ParseProfiles(strings.NewReader(input), &Ignore{})

	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.Equal(t, []ProfileBlock{
		{StartLine: 1, StartCol: 2, EndLine: 3, EndCol: 4, NumStmt: 1, Count: 5},
		{StartLine: 1, StartCol: 2, EndLine: 3, EndCol: 4, NumStmt: 1, Count: 7},
	}, profiles[0].Blocks)
}

func TestParseProfilesValidatesIgnoredLines(t *testing.T) {
	input := "mode: set\nexample.com/project/file.go:999999999999999999999999.2,3.4 1 5\n"
	ignore := &Ignore{Files: regexp.MustCompile(`file\.go$`)}

	profiles, err := ParseProfiles(strings.NewReader(input), ignore)
	require.ErrorContains(t, err, "invalid start line")
	require.Nil(t, profiles)
}

func TestParseProfilesRegressionFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/regression/*/*/coverage.txt")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, fileName := range files {
		t.Run(filepath.ToSlash(fileName), func(t *testing.T) {
			file, err := os.Open(fileName)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, file.Close())
			}()

			profiles, err := ParseProfiles(file, &Ignore{})
			require.NoError(t, err)
			require.NotEmpty(t, profiles)
		})
	}
}

func FuzzParseProfiles(f *testing.F) {
	seeds := [][]byte{
		{},
		[]byte("mode: set\n"),
		[]byte("mode: atomic\r\nexample.com/project/file.go:1.2,3.4 1 5\r\n"),
		[]byte("mode: count\nexample.com/project/file.go:1.2,3.4 1 5\n"),
		[]byte("invalid data"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		profiles, err := ParseProfiles(bytes.NewReader(input), &Ignore{})
		if err != nil && profiles != nil {
			t.Fatalf("ParseProfiles returned profiles with an error: %v", err)
		}
	})
}
