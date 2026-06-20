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
