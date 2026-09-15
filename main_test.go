package main

import (
	"testing"

	"github.com/giantswarm/muster/v5/cmd"
	"github.com/giantswarm/muster/v5/pkg/project"
)

// The root command carries the build identity itself; `muster --version`
// prints it without main wiring anything.
func TestVersionWiring(t *testing.T) {
	if got, want := cmd.RootCommand().Version, project.VersionLine(); got == "" || got != want {
		t.Fatalf("root command version = %q, want %q", got, want)
	}
}
