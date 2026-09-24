package logging

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureStderr points os.Stderr at a file for the rest of the test and
// returns a function that reads what was written to it.
func captureStderr(t *testing.T) func() []byte {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	require.NoError(t, err)
	prev := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = prev
		_ = f.Close()
	})
	return func() []byte {
		data, err := os.ReadFile(f.Name())
		require.NoError(t, err)
		return data
	}
}

func TestInit_OTLPMode_MirrorsToStderr(t *testing.T) {
	tests := []struct {
		name       string
		output     io.Writer
		wantMirror bool
	}{
		{name: "mirrors when output is live", output: os.Stderr, wantMirror: true},
		{name: "stays silent when output is discarded", output: io.Discard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv("OTEL_LOGS_EXPORTER", "none")
			read := captureStderr(t)

			shutdown, err := Init(t.Context(), LevelInfo, tt.output, "muster-test", "0.0.0-test")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, shutdown(context.WithoutCancel(t.Context()))) })

			Info("Test", "hello %s", "mirror")

			out := read()
			if !tt.wantMirror {
				require.Empty(t, out)
				return
			}
			var record map[string]any
			require.NoError(t, json.Unmarshal(out, &record), "stderr line: %s", out)
			require.Equal(t, "hello mirror", record["msg"])
			require.Equal(t, "Test", record["subsystem"])
		})
	}
}
