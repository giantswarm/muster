package jq

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	result := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"name": "web-1", "image": "nginx:1.25"},
			map[string]interface{}{"name": "db-1", "image": "postgres:16"},
		},
	}
	tests := []struct {
		name    string
		program string
		input   map[string]interface{}
		vars    map[string]interface{}
		want    string
		wantErr string
	}{
		{name: "one value", program: `.items | length`, want: "2"},
		{name: "collected stream", program: `[.items[] | .name]`, want: "[web-1 db-1]"},
		{name: "no value is nil", program: `.items[] | select(.name == "none")`, want: "<nil>"},
		{name: "input variable", program: `[.items[] | select(.image | contains($input.image)) | .name]`, input: map[string]interface{}{"image": "nginx"}, want: "[web-1]"},
		// Loop variables may hold Go types gojq does not accept as they are.
		{name: "vars normalized", program: `$vars.n + 1`, vars: map[string]interface{}{"n": int64(41)}, want: "42"},
		{name: "keyword key shorthand", program: `{error: "x"} | {error}`, want: "map[error:x]"},
		{name: "halt yields nothing", program: `halt`, want: "<nil>"},
		{name: "stream is refused", program: `.items[] | .name`, wantErr: "yielded 2 values"},
		{name: "runtime error", program: `.items + 1`, wantErr: "cannot add"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Compile(tt.program)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			got, err := p.Run(context.Background(), result, tt.input, tt.vars)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if s := fmt.Sprint(got); s != tt.want {
				t.Fatalf("got %s, want %s", s, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	if err := Check(`[.items[] |`); err == nil {
		t.Fatal("an unfinished program compiled")
	}
	if err := Check(`$undeclared`); err == nil {
		t.Fatal("a program reading an undeclared variable compiled")
	}
	if err := Check(`[.items[] | select(.x == $input.y)] | length`); err != nil {
		t.Fatalf("a valid program did not compile: %v", err)
	}
}

func TestCacheCompilesOnce(t *testing.T) {
	var c Cache
	a, err := c.Get(`.a`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Get(`.a`)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("the same source compiled twice")
	}
	if _, err := c.Get(`.[`); err == nil {
		t.Fatal("an invalid program was cached")
	}
}
