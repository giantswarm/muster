package strings

import (
	"strings"
)

// DefaultDescriptionMaxLen is the default maximum length for descriptions in formatted output.
// This constant is shared across packages to ensure consistent truncation behavior.
const DefaultDescriptionMaxLen = 60

// MinTruncateLen is the minimum maxLen value for TruncateDescription.
// Values smaller than this would not leave room for meaningful content plus "...".
const MinTruncateLen = 4

// TruncateDescription truncates a string to maxLen characters and ensures single-line output.
// It replaces newlines with spaces, collapses multiple whitespace characters into single spaces,
// and adds "..." if truncated.
//
// The function handles Unicode correctly by operating on runes rather than bytes,
// preventing truncation in the middle of multi-byte characters.
//
// If maxLen is less than MinTruncateLen (4), it is clamped to MinTruncateLen to ensure
// there is room for at least one character plus "...".
//
// Args:
//   - s: The string to truncate
//   - maxLen: Maximum length of the result (including "..." if truncated)
//
// Returns:
//   - Truncated and sanitized string
func TruncateDescription(s string, maxLen int) string {
	// Clamp maxLen to minimum value to prevent panic from negative slice index
	if maxLen < MinTruncateLen {
		maxLen = MinTruncateLen
	}

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
