// Package main renders the CLI reference in docs/reference/cli from the Cobra
// command tree, so the reference cannot drift from the commands themselves.
//
// Usage:
//
//	go run ./hack/gen-cli-docs [output-dir]
//
// The default output directory is docs/reference/cli. Every visible command
// becomes one page; the root command becomes README.md so the directory reads
// as an index on GitHub and as the section landing page on the documentation
// site. `make generate-cli-docs` runs it and `make verify-cli-docs` fails when
// the committed pages are stale.
package main
