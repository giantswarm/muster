package main

import "muster/cmd"

// Version can be set during build with -ldflags
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
