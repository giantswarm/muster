// Package project carries the build identity of the muster binary: its
// version, git commit and build time, and the names that identify the project
// in messages. The generated Makefile (devctl) and the architect CI stamp the
// identifiers through `-ldflags -X`; a plain `go build` or `go install` has no
// ldflags and falls back to the module build info, which Go 1.24+ derives from
// the VCS tag of the checkout. The package has no dependencies, so `main`, any
// CLI command and the aggregator can import it.
package project
