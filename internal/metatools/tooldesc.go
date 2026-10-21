package metatools

import "github.com/giantswarm/mcp-toolkit/tooldesc"

// toolDescription joins a one-line summary and its details into a meta-tool
// description with tooldesc.Description, the summary on the first line. It
// panics on a summary tooldesc refuses, so a test that lists the meta-tools
// fails.
func toolDescription(summary, details string) string {
	d, err := tooldesc.Description(summary, details)
	if err != nil {
		panic(err)
	}
	return d
}
