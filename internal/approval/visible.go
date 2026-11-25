package approval

import (
	"fmt"
	"strings"
	"unicode"
)

// Visible writes s so that what the page shows is what is stored: every
// character that is invisible or changes how text around it reads -- bidi
// controls and other format characters (Unicode Cf: zero-width spaces and
// joiners, the byte order mark), C0 and C1 controls (line breaks included),
// the line and paragraph separators -- is replaced by a visible marker such
// as ⟨U+202E⟩. An agent chooses its arguments; it must not be able to make a
// value read differently from what runs (M1-MU-S-06).
func Visible(s string) string {
	if !strings.ContainsFunc(s, hidden) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if hidden(r) {
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hidden(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.IsControl(r) || r == ' ' || r == ' ' || r == unicode.ReplacementChar
}
