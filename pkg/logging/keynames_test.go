package logging

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyNames(t *testing.T) {
	require.Equal(t, "none", KeyNames[any](nil))
	require.Equal(t, "none", KeyNames(map[string]any{}))
	require.Equal(t, "a, b, c", KeyNames(map[string]any{"c": 1, "a": "x", "b": nil}))
	require.Equal(t, "token, user", KeyNames(map[string]string{"user": "alice", "token": "eyJsecret"}))
}

func TestShape(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "nil"},
		{"empty object", map[string]any{}, "object{}"},
		{"object", map[string]any{"token": "eyJsecret", "status": "ok"}, "object{status, token}"},
		{"array", []any{"eyJsecret", 2}, "array(len=2)"},
		{"string", "eyJsecret-marker", "string(len=16)"},
		{"number", 3.5, "float64"},
		{"bool", true, "bool"},
		{"typed slice", []string{"eyJsecret"}, "[]string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Shape(tc.in)
			require.Equal(t, tc.want, got)
			require.NotContains(t, got, "eyJ", "Shape must never print values")
		})
	}
}
