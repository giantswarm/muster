package strings

import (
	"strings"
)

// TruncateDescription truncates a string to maxLen characters and ensures single-line output.
// It replaces newlines with spaces, collapses multiple whitespace characters into single spaces,
// and adds "..." if truncated.
//
// The function handles Unicode correctly by operating on runes rather than bytes,
// preventing truncation in the middle of multi-byte characters.
//
// Args:
//   - s: The string to truncate
//   - maxLen: Maximum length of the result (including "..." if truncated)
//
// Returns:
//   - Truncated and sanitized string
func TruncateDescription(s string, maxLen int) string {
	// Use strings.Fields to split on any whitespace (handles \n, \r, \t, multiple spaces)
	// then rejoin with single spaces. This is more efficient than multiple ReplaceAll calls.
	s = strings.Join(strings.Fields(s), " ")

	// Use rune-based slicing to handle Unicode correctly
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen-3]) + "..."
	}
	return s
}
