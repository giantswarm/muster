package config

import (
	"testing"
	"time"
)

func TestDurationFromEnv(t *testing.T) {
	const name = "MUSTER_TEST_DURATION_FROM_ENV"
	def := 30 * time.Second

	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "unset keeps the default", value: "", want: def},
		{name: "a valid duration wins", value: "2s", want: 2 * time.Second},
		{name: "an unparsable value keeps the default", value: "soon", want: def},
		{name: "zero keeps the default", value: "0s", want: def},
		{name: "a negative value keeps the default", value: "-1s", want: def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(name, tc.value)
			if got := DurationFromEnv(name, def); got != tc.want {
				t.Fatalf("DurationFromEnv(%q=%q) = %v, want %v", name, tc.value, got, tc.want)
			}
		})
	}
}
