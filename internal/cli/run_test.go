package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunDoesNotRetainOptions(t *testing.T) {
	root := repositoryRoot(t)
	input, err := os.ReadFile(filepath.Join(root, "testdata", "testdata_set.txt"))
	require.NoError(t, err)

	var byFilesOutput bytes.Buffer
	require.Zero(t, Run(
		[]string{"-by-files", "-path", root},
		bytes.NewReader(input),
		&byFilesOutput,
		io.Discard,
	))
	require.Contains(t, byFilesOutput.String(), `name="testdata.func1.go"`)

	var defaultOutput bytes.Buffer
	require.Zero(t, Run([]string{"-path", root}, bytes.NewReader(input), &defaultOutput, io.Discard))
	require.Contains(t, defaultOutput.String(), `name="-"`)
	require.NotContains(t, defaultOutput.String(), `name="testdata.func1.go"`)
}

func TestRunRejectsInvalidIgnorePattern(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := Run(
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

	exitCode := Run([]string{"-help"}, strings.NewReader("mode: set"), io.Discard, &stderr)

	require.Zero(t, exitCode)
	require.Contains(t, stderr.String(), "Usage of go-cobertura")
}

func TestRunAcceptsEmptyInput(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(nil, strings.NewReader(""), &stdout, &stderr)

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

			exitCode := Run(test.args, strings.NewReader(test.input), &stdout, &stderr)

			require.Equal(t, 1, exitCode)
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), test.wantStderr)
		})
	}
}

func TestRunReportsOutputWriterFailure(t *testing.T) {
	stdout := &failOnceWriter{}
	var stderr bytes.Buffer

	exitCode := Run(nil, strings.NewReader("mode: set"), stdout, &stderr)

	require.Equal(t, 1, exitCode)
	require.True(t, stdout.failed)
	require.Contains(t, stderr.String(), "code coverage conversion failed: writing xml header")
	require.Contains(t, stderr.String(), errTestWrite.Error())
}

var errTestWrite = errors.New("test write failure")

type failOnceWriter struct {
	failed bool
}

func (w *failOnceWriter) Write(data []byte) (int, error) {
	if !w.failed {
		w.failed = true
		return 0, errTestWrite
	}
	return len(data), nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}
