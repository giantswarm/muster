package approval

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The canonical form follows RFC 8785: keys sorted by UTF-16 code units,
// ECMAScript numbers, minimal string escaping.
func TestCanonical(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty", nil, `{}`},
		{"key order", map[string]any{"b": 1.0, "a": "x", "c": map[string]any{"z": true, "y": nil}}, `{"a":"x","b":1,"c":{"y":null,"z":true}}`},
		{"utf16 order", map[string]any{"\u20ac": 1.0, "\r": 2.0, "\ufb33": 3.0, "1": 4.0, "\U0001f600": 5.0, "\u0080": 6.0, "\u00f6": 7.0}, "{\"\\r\":2,\"1\":4,\"\u0080\":6,\"\u00f6\":7,\"\u20ac\":1,\"\U0001f600\":5,\"\ufb33\":3}"},
		{"numbers", map[string]any{"a": 333333333.33333329, "b": 1e30, "c": 4.5, "d": 2e-3, "e": 0.000001, "f": 1e-7, "g": -0.0, "h": 1e21, "i": 1e20}, `{"a":333333333.3333333,"b":1e+30,"c":4.5,"d":0.002,"e":0.000001,"f":1e-7,"g":0,"h":1e+21,"i":100000000000000000000}`},
		{"escaping", map[string]any{"s": "\"\\\b\f\n\r\t\x01 <b>é</b> \u2028"}, "{\"s\":\"\\\"\\\\\\b\\f\\n\\r\\t\\u0001 <b>é</b> \u2028\"}"},
		{"arrays and json numbers", map[string]any{"a": []any{json.Number("10"), 1, int64(2), "x"}}, `{"a":[10,1,2,"x"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, hash, err := Canonical(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, Hash(tc.want), hash)
		})
	}
}

// The canonical form of decoded arguments survives a store round trip: what
// is shown is what is dispatched.
func TestCanonical_RoundTrip(t *testing.T) {
	args := map[string]any{"id": 9007199254740993.0, "nested": map[string]any{"list": []any{1.5, "two"}}, "note": "line\nbreak"}
	canonical, hash, err := Canonical(args)
	require.NoError(t, err)
	decoded, err := DecodeCanonical(canonical)
	require.NoError(t, err)
	again, againHash, err := Canonical(decoded)
	require.NoError(t, err)
	assert.Equal(t, canonical, again)
	assert.Equal(t, hash, againHash)
}

func TestCanonical_Refused(t *testing.T) {
	_, _, err := Canonical(map[string]any{"n": math.NaN()})
	require.Error(t, err)
	_, _, err = Canonical(map[string]any{"n": math.Inf(1)})
	require.Error(t, err)
	_, _, err = Canonical(map[string]any{"big": strings.Repeat("x", MaxArgumentsSize)})
	require.ErrorContains(t, err, "more than")
}
