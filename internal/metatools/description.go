package metatools

import "strings"

// splitDescription splits a tool description into its summary, the first line
// that is not blank, trimmed, and its details, everything after that line.
// The ranker weighs the two separately, and the discovery tier shows the
// summary.
func splitDescription(desc string) (summary, details string) {
	rest := desc
	for rest != "" {
		line, after, _ := strings.Cut(rest, "\n")
		if s := strings.TrimSpace(line); s != "" {
			return s, after
		}
		rest = after
	}
	return "", ""
}

// summarizeText returns the summary of desc (see splitDescription), capped to
// maxRunes runes (maxRunes <= 0 means no cap). It is the cheap one-line excerpt
// the discovery tier returns in place of a full description.
func summarizeText(desc string, maxRunes int) string {
	s, _ := splitDescription(desc)
	if maxRunes > 0 {
		if r := []rune(s); len(r) > maxRunes {
			s = strings.TrimSpace(string(r[:maxRunes])) + "..."
		}
	}
	return s
}
