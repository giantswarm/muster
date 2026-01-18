package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommandFlags_ToExecutorOptions_ValidatesFormat(t *testing.T) {
	tests := []struct {
		name         string
		outputFormat string
		wantErr      bool
		errMsg       string
	}{
		{
			name:         "valid table format",
			outputFormat: "table",
			wantErr:      false,
		},
		{
			name:         "valid wide format",
			outputFormat: "wide",
			wantErr:      false,
		},
		{
			name:         "valid json format",
			outputFormat: "json",
			wantErr:      false,
		},
		{
			name:         "valid yaml format",
			outputFormat: "yaml",
			wantErr:      false,
		},
		{
			name:         "invalid format returns error",
			outputFormat: "invalid",
			wantErr:      true,
			errMsg:       "unsupported output format",
		},
		{
			name:         "empty format returns error",
			outputFormat: "",
			wantErr:      true,
			errMsg:       "unsupported output format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := &CommandFlags{
				OutputFormat: tt.outputFormat,
			}
			opts, err := flags.ToExecutorOptions()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, OutputFormat(tt.outputFormat), opts.Format)
			}
		})
	}
}
