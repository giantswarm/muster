// Command gen rewrites the scale fixture's YAML files from the generator, so
// `go generate ./internal/testing/fixtures/scale/` is the one way to change
// the committed fixture.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giantswarm/muster/v5/internal/testing/fixtures/scale"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	f := scale.Get()
	pre, err := scale.RenderPreConfiguration(f)
	if err != nil {
		fail(err)
	}
	sessions, err := scale.RenderSessions(f)
	if err != nil {
		fail(err)
	}
	for name, data := range map[string][]byte{"pre_configuration.yaml": pre, "sessions.yaml": sessions} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil { //nolint:gosec // a committed fixture, world-readable on purpose
			fail(err)
		}
	}
	shape := f.Shape()
	fmt.Printf("scale fixture: %d servers (%d session-authenticated), %d documents, %d workflows, %d sessions\n",
		shape.Servers, shape.SessionAuthServers, shape.Documents, shape.Workflows, shape.Sessions)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}
