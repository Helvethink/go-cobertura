package main

import (
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
