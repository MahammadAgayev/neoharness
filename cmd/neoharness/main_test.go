package main

import (
	"slices"
	"testing"
)

func TestCommandForReadsTheEnvironment(t *testing.T) {
	const key = "NEOHARNESS_TEST_COMMAND"
	for _, tc := range []struct {
		env  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"authwrap agent run claude", []string{"authwrap", "agent", "run", "claude"}},
	} {
		t.Setenv(key, tc.env)
		if got := commandFor(key); !slices.Equal(got, tc.want) {
			t.Errorf("commandFor with %q = %q, want %q", tc.env, got, tc.want)
		}
	}
}
