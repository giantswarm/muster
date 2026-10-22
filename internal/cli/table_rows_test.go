package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleetImageScanResult is the shape workflow_fleet-image-scan returns: a few
// properties beside a "matches" array of row objects.
const fleetImageScanResult = `{
  "clusters_scanned": 4,
  "execution_id": "e77072f4-a18c-47f2-9210-1e8abe7966ce",
  "failures": [],
  "image": "quay.io/giantswarm/",
  "matches": [
    {"cluster": "gazelle", "images": ["quay.io/giantswarm/external-dns:v0.11.0"], "kind": "ReplicaSet",
     "management_cluster": "gazelle", "name": "external-dns-5b555b6c56", "namespace": "kube-system",
     "owner": "Deployment/external-dns", "replicas": 0},
    {"cluster": "gazelle", "images": ["quay.io/giantswarm/a:1", "quay.io/giantswarm/b:2", "quay.io/giantswarm/c:3"],
     "kind": "DaemonSet", "management_cluster": "gazelle", "name": "node-agent", "namespace": "monitoring", "owner": ""}
  ]
}`

// captureStdout returns what fn prints to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	require.NoError(t, w.Close())
	return <-done
}

func formatJSON(t *testing.T, format OutputFormat, text string) string {
	t.Helper()
	var data interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &data))
	return captureStdout(t, func() {
		require.NoError(t, NewTableFormatter(ExecutorOptions{Format: format}).FormatData(data))
	})
}

func TestFormatData_ResultRowsRenderAsTable(t *testing.T) {
	for _, format := range []OutputFormat{OutputFormatTable, OutputFormatWide} {
		t.Run(string(format), func(t *testing.T) {
			out := formatJSON(t, format, fleetImageScanResult)

			assert.NotContains(t, out, "items]")
			assert.Contains(t, out, "matches (2):")
			// Every key of the rows is a column, in alphabetical order.
			assert.Regexp(t, `CLUSTER\s+IMAGES\s+KIND\s+MANAGEMENT_CLUSTER\s+NAME\s+NAMESPACE\s+OWNER\s+REPLICAS`, out)
			// Rows keep the result's order; lists are joined in full.
			assert.Regexp(t, `(?s)external-dns-5b555b6c56.*node-agent`, out)
			assert.Contains(t, out, "quay.io/giantswarm/a:1, quay.io/giantswarm/b:2, quay.io/giantswarm/c:3")
			assert.Regexp(t, `gazelle\s+quay.io/giantswarm/external-dns:v0.11.0\s+ReplicaSet\s+gazelle\s+external-dns-5b555b6c56\s+kube-system\s+Deployment/external-dns\s+0`, out)
			// A key missing from a row, or an empty value, is "-".
			assert.Regexp(t, `node-agent\s+monitoring\s+-\s+-`, out)
			// The other fields stay a property table above the rows.
			assert.Regexp(t, `(?s)PROPERTY\s+VALUE.*clusters_scanned\s+4.*image\s+quay.io/giantswarm/.*matches \(2\):`, out)
		})
	}
}

func TestFormatData_RowsOnlyObject(t *testing.T) {
	out := formatJSON(t, OutputFormatTable, `{"matches": [{"name": "a"}], "failures": [{"cluster": "x", "error": "boom"}]}`)

	assert.NotContains(t, out, "PROPERTY")
	assert.Regexp(t, `(?s)^failures \(1\):\nCLUSTER\s+ERROR\nx\s+boom\n\nmatches \(1\):\nNAME\na\n$`, out)
}

func TestFormatData_RowsNoHeaders(t *testing.T) {
	var data interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"matches": [{"name": "a", "kind": "Pod"}]}`), &data))
	out := captureStdout(t, func() {
		require.NoError(t, NewTableFormatter(ExecutorOptions{Format: OutputFormatTable, NoHeaders: true}).FormatData(data))
	})

	assert.NotContains(t, out, "KIND")
	assert.Contains(t, out, "Pod")
}

func TestFormatData_MCPServerKeepsPropertyTable(t *testing.T) {
	out := formatJSON(t, OutputFormatTable, `{"name": "k8s", "type": "streamable-http", "state": "connected", "sessions": [{"name": "a"}, {"name": "b"}, {"name": "c"}]}`)

	assert.Contains(t, out, "PROPERTY")
	assert.NotContains(t, out, "sessions (3):")
}

func TestIsRowArray(t *testing.T) {
	assert.True(t, isRowArray([]interface{}{map[string]interface{}{"a": 1}}))
	assert.False(t, isRowArray([]interface{}{}))
	assert.False(t, isRowArray(nil))
	assert.False(t, isRowArray([]interface{}{"a", "b"}))
	assert.False(t, isRowArray([]interface{}{map[string]interface{}{"a": 1}, "b"}))
	assert.False(t, isRowArray(map[string]interface{}{"a": 1}))
}

func TestFormatRowCell(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  string
	}{
		{"nil", nil, "-"},
		{"empty string", "", "-"},
		{"string", "Pod", "Pod"},
		{"number", float64(3), "3"},
		{"bool", true, "true"},
		{"empty list", []interface{}{}, "-"},
		{"list", []interface{}{"a", float64(2), "c"}, "a, 2, c"},
		{"object", map[string]interface{}{"b": float64(1), "a": "x"}, `{"a":"x","b":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatRowCell(tt.value))
		})
	}
}

func TestWriteRowTable_AllRowsAllColumns(t *testing.T) {
	var buf bytes.Buffer
	rows := []interface{}{
		map[string]interface{}{"b": "1"},
		map[string]interface{}{"a": "2", "c": []interface{}{"x", "y", "z"}},
	}
	NewTableFormatter(ExecutorOptions{}).writeRowTable(&buf, rows)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Regexp(t, `^A\s+B\s+C$`, lines[0])
	assert.Regexp(t, `^-\s+1\s+-$`, lines[1])
	assert.Regexp(t, `^2\s+-\s+x, y, z$`, lines[2])
}
