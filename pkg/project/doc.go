// Package project exposes the build identifiers populated by the architect-orb
// `go-build` job at link time (gitSHA, buildTimestamp). It has no dependencies
// so it can be safely imported by `main` and any CLI command.
package project
