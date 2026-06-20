package template

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineResolvePath(t *testing.T) {
	root := map[string]interface{}{
		"data": map[string]interface{}{
			"field": "value",
			"items": []interface{}{
				map[string]interface{}{"name": "alpha"},
				map[string]interface{}{"name": "beta"},
			},
		},
		"matrix": []interface{}{
			[]interface{}{"a", "b"},
			[]interface{}{"c", "d"},
		},
	}

	e := New()

	cases := []struct {
		name    string
		path    string
		want    interface{}
		wantErr bool
	}{
		{"object navigation", "data.field", "value", false},
		{"array index then field", "data.items[1].name", "beta", false},
		{"whole array preserved", "data.items", root["data"].(map[string]interface{})["items"], false},
		{"chained indices", "matrix[1][0]", "c", false},
		{"missing key", "data.missing", nil, true},
		{"index on non-array", "data.field[0]", nil, true},
		{"index out of range", "data.items[5]", nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.ResolvePath(root, tc.path)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// RenderGoTemplateTyped must preserve the real Go type of a single-action
// template and, crucially, never coerce a numeric-looking string (version
// "1.20", zero-padded "08") into a number. Mixed text/action templates render
// to a string.
func TestEngineRenderGoTemplateTyped(t *testing.T) {
	e := New()
	ctx := map[string]interface{}{
		"version": "1.20",
		"padded":  "08",
		"count":   float64(8),
		"items":   []interface{}{"a", "b", "c"},
		"name":    "muster",
	}

	cases := []struct {
		name string
		tmpl string
		want interface{}
	}{
		// The footgun: computed strings that look numeric must stay strings.
		{"version string stays string", "{{ .version }}", "1.20"},
		{"zero-padded computed stays string", `{{ printf "%02d" (int .count) }}`, "08"},
		{"version via printf stays string", `{{ printf "%s" .version }}`, "1.20"},
		// Genuine numbers stay numbers.
		{"len yields a number", "{{ len .items }}", 3},
		{"arithmetic yields a number", "{{ add 40 2 }}", int64(42)},
		// Booleans and plain strings.
		{"eq yields a bool", `{{ eq .name "muster" }}`, true},
		{"upper yields a string", "{{ .name | upper }}", "MUSTER"},
		// Non-finite strings survive (they would break JSON if turned to float).
		{"NaN stays a string", `{{ printf "%s" "NaN" }}`, "NaN"},
		// Mixed literal text + action is inherently textual.
		{"mixed text renders to string", "v{{ .version }}", "v1.20"},
		{"all-digit mixed text stays string", "{{ .padded }}{{ .padded }}", "0808"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.RenderGoTemplateTyped(tc.tmpl, ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
