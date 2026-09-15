package main

import (
	"testing"

	"github.com/giantswarm/muster/v5/cmd"
	"github.com/giantswarm/muster/v5/pkg/project"
)

func TestVersionWiring(t *testing.T) {
	cmd.SetVersion(project.Version())
	if got := cmd.GetVersion(); got == "" {
		t.Fatal("cmd.GetVersion() returned empty after wiring through project.Version()")
	}
}
