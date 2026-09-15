package main

import (
	"github.com/giantswarm/muster/v5/cmd"
	"github.com/giantswarm/muster/v5/pkg/project"
)

func main() {
	cmd.SetVersion(project.Version())
	cmd.Execute()
}
