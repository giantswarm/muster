package approval

import (
	"fmt"
	"strings"
	"unicode"
)

// Visible writes s so that what the page shows is what is stored: every
// character that is invisible, blank or changes how text around it reads --
// bidi controls and other format characters (Unicode Cf: zero-width spaces
// and joiners, the byte order mark), C0 and C1 controls (line breaks
// included), the line and paragraph separators, spaces other than the ASCII
// space, variation selectors and other default-ignorable characters, blank
// fillers -- is replaced by a visible marker such
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
	switch {
	case unicode.Is(unicode.Cf, r), unicode.IsControl(r), r == '\u2028', r == '\u2029', r == unicode.ReplacementChar:
		return true
	case unicode.Is(unicode.Zs, r) && r != ' ':
		// No-break and other spaces read as the ASCII space (re-check R-S-03).
		return true
	case unicode.Is(unicode.Variation_Selector, r), unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r):
		// Variation selectors, U+034F, the Hangul fillers (U+115F, U+1160,
		// U+3164, U+FFA0) and other characters that render as nothing.
		return true
	case r == '\u2800':
		// The blank braille pattern renders as a space.
		return true
	}
	return false
}
