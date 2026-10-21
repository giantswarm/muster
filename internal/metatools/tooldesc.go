package metatools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The budgets of giantswarm/giantswarm#38592 beyond the summary line
// (summaryMaxLen), as mcp-toolkit's tooldesc package enforces them. Once
// muster imports tooldesc (#1545), toolDescription becomes
// tooldesc.Description and the budget test tooldesc.CheckServer.
const (
	// descriptionMax caps a whole description, summary and details.
	descriptionMax = 600
	// paramMax caps every argument description.
	paramMax = 150
	// prefixMax is how many leading runes no two descriptions may share.
	prefixMax = 40
)

// toolDescription joins a one-line summary and its details into a meta-tool
// description: the summary on the first line, so a client that shows one
// line shows it whole. It panics on a summary over summaryMaxLen runes or with
// a line break, so a test that lists the meta-tools fails.
func toolDescription(summary, details string) string {
	s := strings.TrimSpace(summary)
	if n := utf8.RuneCountInString(s); n == 0 || n > summaryMaxLen || strings.Contains(s, "\n") {
		panic(fmt.Sprintf("meta-tool summary %q must be one line of 1 to %d runes", s, summaryMaxLen))
	}
	if details = strings.TrimSpace(details); details == "" {
		return s
	}
	return s + "\n\n" + details
}
