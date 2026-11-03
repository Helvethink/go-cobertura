package cobertura

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHitRate(t *testing.T) {
	tests := []struct {
		name string
		rate func() float32
	}{
		{
			name: "empty lines",
			rate: func() float32 {
				lines := Lines{}
				return lines.HitRate()
			},
		},
		{
			name: "empty method",
			rate: func() float32 {
				return (Method{}).HitRate()
			},
		},
		{
			name: "empty class",
			rate: func() float32 {
				return (Class{}).HitRate()
			},
		},
		{
			name: "empty package",
			rate: func() float32 {
				return (Package{}).HitRate()
			},
		},
		{
			name: "empty coverage",
			rate: func() float32 {
				return (&Coverage{}).HitRate()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Zero(t, test.rate())
		})
	}
}

func TestHitRateWithLines(t *testing.T) {
	lines := Lines{
		&Line{Number: 1, Hits: 1},
		&Line{Number: 2, Hits: 0},
	}

	require.InDelta(t, 0.5, lines.HitRate(), 0)
}

func TestLinesAddOrUpdateLine(t *testing.T) {
	lines := Lines{}
	lines.AddOrUpdateLine(3, 1, "count")
	lines.AddOrUpdateLine(1, 2, "count")
	lines.AddOrUpdateLine(2, 0, "set")
	lines.AddOrUpdateLine(1, 3, "count")
	lines.AddOrUpdateLine(2, 1, "set")

	require.Equal(t, Lines{
		&Line{Number: 1, Hits: 5},
		&Line{Number: 2, Hits: 1},
		&Line{Number: 3, Hits: 1},
	}, lines)
}

func BenchmarkLinesAddOrUpdateLine(b *testing.B) {
	const lineCount = 10_000

	b.Run("unique", func(b *testing.B) {
		for b.Loop() {
			lines := make(Lines, 0, lineCount)
			for lineNumber := 1; lineNumber <= lineCount; lineNumber++ {
				lines.AddOrUpdateLine(lineNumber, 1, "set")
			}
		}
	})

	b.Run("overlapping", func(b *testing.B) {
		for b.Loop() {
			lines := make(Lines, 0, lineCount/2)
			for lineNumber := 1; lineNumber <= lineCount; lineNumber++ {
				lines.AddOrUpdateLine((lineNumber+1)/2, 1, "count")
			}
		}
	})
}
