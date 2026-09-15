package cmd

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

// TestParseDynamicArgs runs command lines through the real start and call
// commands the way cobra does -- ParseFlags with unknown flags allowed -- and
// checks that the two readings account for every token: cobra keeps the flags
// the command declares and the positional arguments, parseDynamicArgs gets the
// rest. The first cases are the leak of #1249: the connection flags given after
// the workflow or tool name reached the workflow as input.
func TestParseDynamicArgs(t *testing.T) {
	tests := []struct {
		name        string
		cmd         *cobra.Command
		coerce      func(string) interface{}
		args        []string // the command line after the binary name
		positionals []string // what cobra is left with
		flag, value string   // a declared flag and the value cobra parsed for it
		want        map[string]interface{}
	}{
		{
			name:   "start workflow: connection flags after the workflow name are not workflow input",
			cmd:    startCmd,
			coerce: stringValue,
			args: []string{"start", "workflow", "inspect-directory", "--path=/tmp/data", "--file=note.txt",
				"--endpoint", "http://localhost:38092/mcp", "--auth", "none"},
			positionals: []string{"workflow", "inspect-directory"},
			flag:        "endpoint",
			value:       "http://localhost:38092/mcp",
			want:        map[string]interface{}{"path": "/tmp/data", "file": "note.txt"},
		},
		{
			name:   "start workflow: every declared flag is skipped with its value, inline, spaced, shorthand or boolean",
			cmd:    startCmd,
			coerce: stringValue,
			args: []string{"start", "workflow", "w", "--a=1", "--output", "json", "-o", "wide", "--quiet", "-q",
				"--debug", "--no-headers", "--config-path", "/c", "--context", "ctx", "--endpoint=http://x",
				"--auth=none", "--b", "2"},
			positionals: []string{"workflow", "w"},
			flag:        "output",
			value:       "wide",
			want:        map[string]interface{}{"a": "1", "b": "2"},
		},
		{
			name:   "start workflow: after -- every flag is a workflow argument, a declared name included",
			cmd:    startCmd,
			coerce: stringValue,
			args: []string{"start", "workflow", "sync", "--endpoint", "http://x", "--auth", "none", "--",
				"--endpoint=https://target", "--auth", "basic"},
			positionals: []string{"workflow", "sync", "--endpoint=https://target", "--auth", "basic"},
			flag:        "endpoint",
			value:       "http://x",
			want:        map[string]interface{}{"endpoint": "https://target", "auth": "basic"},
		},
		{
			name:        "start workflow: a bare flag is true, a negative number is a value, a shorthand is not",
			cmd:         startCmd,
			coerce:      stringValue,
			args:        []string{"start", "workflow", "w", "--force", "--offset", "-5", "--name", "-q"},
			positionals: []string{"workflow", "w"},
			want:        map[string]interface{}{"force": "true", "offset": "-5", "name": "true"},
		},
		{
			name:        "start workflow: an argument given before the workflow name counts too",
			cmd:         startCmd,
			coerce:      stringValue,
			args:        []string{"start", "workflow", "--path", "/x", "w"},
			positionals: []string{"workflow", "w"},
			want:        map[string]interface{}{"path": "/x"},
		},
		{
			name:        "start workflow: a declared flag takes the next token as pflag does, even one spelled like a flag",
			cmd:         startCmd,
			coerce:      stringValue,
			args:        []string{"start", "workflow", "w", "--context", "--odd", "--a=1"},
			positionals: []string{"workflow", "w"},
			flag:        "context",
			value:       "--odd",
			want:        map[string]interface{}{"a": "1"},
		},
		{
			name:        "start service: nothing to forward",
			cmd:         startCmd,
			coerce:      stringValue,
			args:        []string{"start", "service", "prometheus", "--endpoint", "http://x"},
			positionals: []string{"service", "prometheus"},
			want:        map[string]interface{}{},
		},
		{
			name:   "call: connection flags after the tool name are not tool arguments",
			cmd:    callCmd,
			coerce: coerceValue,
			args: []string{"call", "core_service_status", "--name=prometheus", "--endpoint", "http://x",
				"--auth", "none", "--debug"},
			positionals: []string{"core_service_status"},
			flag:        "auth",
			value:       "none",
			want:        map[string]interface{}{"name": "prometheus"},
		},
		{
			name:   "call: values are coerced",
			cmd:    callCmd,
			coerce: coerceValue,
			args: []string{"call", "t", "--replicas=3", "--enabled=true", "--rate", "1.5", "--nothing=null",
				"--version=v1.2.3", "--dry-run"},
			positionals: []string{"t"},
			want: map[string]interface{}{"replicas": int64(3), "enabled": true, "rate": 1.5, "nothing": nil,
				"version": "v1.2.3", "dry-run": true},
		},
		{
			name:        "call: --json is call's own flag, not a tool argument",
			cmd:         callCmd,
			coerce:      coerceValue,
			args:        []string{"call", "t", "--json", `{"x":1}`, "--name=bar"},
			positionals: []string{"t"},
			flag:        "json",
			value:       `{"x":1}`,
			want:        map[string]interface{}{"name": "bar"},
		},
		{
			name:        "call: after -- every flag is a tool argument",
			cmd:         callCmd,
			coerce:      coerceValue,
			args:        []string{"call", "x_http_get", "--endpoint", "http://x", "--", "--endpoint=https://target", "--output", "raw"},
			positionals: []string{"x_http_get", "--endpoint=https://target", "--output", "raw"},
			flag:        "endpoint",
			value:       "http://x",
			want:        map[string]interface{}{"endpoint": "https://target", "output": "raw"},
		},
		{
			name:        "call: -o json and a bare -- leave nothing",
			cmd:         callCmd,
			coerce:      coerceValue,
			args:        []string{"call", "t", "-o", "json", "--"},
			positionals: []string{"t"},
			want:        map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cmd.ParseFlags(tt.args[1:]); err != nil {
				t.Fatalf("cobra rejected %v: %v", tt.args, err)
			}
			if got := tt.cmd.Flags().Args(); !reflect.DeepEqual(got, tt.positionals) {
				t.Errorf("cobra positionals = %v, want %v", got, tt.positionals)
			}
			if tt.flag != "" {
				if got := tt.cmd.Flags().Lookup(tt.flag).Value.String(); got != tt.value {
					t.Errorf("cobra --%s = %q, want %q", tt.flag, got, tt.value)
				}
			}
			if got := parseDynamicArgs(tt.cmd, tt.args, tt.coerce); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDynamicArgs(%v) = %#v, want %#v", tt.args, got, tt.want)
			}
		})
	}
}
