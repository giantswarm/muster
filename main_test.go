package main

import (
	"testing"

	"github.com/giantswarm/muster/cmd"
	"github.com/giantswarm/muster/pkg/project"
)

func TestVersionWiring(t *testing.T) {
	cmd.SetVersion(project.Version())
	if got := cmd.GetVersion(); got == "" {
		t.Fatal("cmd.GetVersion() returned empty after wiring through project.Version()")
	}
}
