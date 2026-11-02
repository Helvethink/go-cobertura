package main

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
