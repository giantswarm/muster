package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeJSONArgs(t *testing.T) {
	properties := map[string]interface{}{
		"management_clusters": map[string]interface{}{"type": "array"},
		"labels":              map[string]interface{}{"type": "object"},
		"nullable_list":       map[string]interface{}{"type": []interface{}{"array", "null"}},
		"filter":              map[string]interface{}{"type": "string"},
		"no_type":             map[string]interface{}{"description": "untyped"},
	}

	tests := []struct {
		name  string
		param string
		value interface{}
		want  interface{}
	}{
		{"array parameter", "management_clusters", `["gazelle","alba"]`, []interface{}{"gazelle", "alba"}},
		{"array with spaces", "management_clusters", ` [ "a" ] `, []interface{}{"a"}},
		{"object parameter", "labels", `{"team":"x","n":1}`, map[string]interface{}{"team": "x", "n": float64(1)}},
		{"type list", "nullable_list", `[1,2]`, []interface{}{float64(1), float64(2)}},
		{"string parameter keeps JSON-looking value", "filter", `[1]`, `[1]`},
		{"untyped parameter keeps value", "no_type", `["a"]`, `["a"]`},
		{"unknown parameter keeps value", "other", `["a"]`, `["a"]`},
		{"invalid JSON stays a string", "management_clusters", `[gazelle]`, `[gazelle]`},
		{"object for array parameter stays a string", "management_clusters", `{"a":1}`, `{"a":1}`},
		{"array for object parameter stays a string", "labels", `["a"]`, `["a"]`},
		{"plain string untouched", "management_clusters", "gazelle", "gazelle"},
		{"non-string untouched", "management_clusters", int64(3), int64(3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{tt.param: tt.value}
			decodeJSONArgs(params, properties)
			assert.Equal(t, tt.want, params[tt.param])
		})
	}
}

func TestDecodeJSONArgs_NilSchema(t *testing.T) {
	params := map[string]interface{}{"list": `["a"]`}
	decodeJSONArgs(params, nil)
	assert.Equal(t, `["a"]`, params["list"])
}

func TestHasJSONLikeValue(t *testing.T) {
	assert.True(t, hasJSONLikeValue(map[string]interface{}{"a": "x", "b": `["y"]`}))
	assert.True(t, hasJSONLikeValue(map[string]interface{}{"a": ` {"k":1}`}))
	assert.False(t, hasJSONLikeValue(map[string]interface{}{"a": "x", "b": int64(1), "c": true}))
	assert.False(t, hasJSONLikeValue(map[string]interface{}{}))
}
