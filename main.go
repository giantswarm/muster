package main

import (
	"github.com/giantswarm/muster/cmd"
	"github.com/giantswarm/muster/pkg/project"
)

func main() {
	cmd.SetVersion(project.Version())
	cmd.Execute()
}
