package strings

import (
	"testing"
)

func TestTruncateDescription(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string unchanged",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "exact length unchanged",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "long string truncated",
			input:    "hello world this is a long string",
			maxLen:   15,
			expected: "hello world ...",
		},
		{
			name:     "newlines replaced with spaces",
			input:    "hello\nworld",
			maxLen:   20,
			expected: "hello world",
		},
		{
			name:     "multiple newlines collapsed",
			input:    "hello\n\n\nworld",
			maxLen:   20,
			expected: "hello world",
		},
		{
			name:     "carriage returns handled",
			input:    "hello\r\nworld",
			maxLen:   20,
			expected: "hello world",
		},
		{
			name:     "multiple spaces collapsed",
			input:    "hello    world",
			maxLen:   20,
			expected: "hello world",
		},
		{
			name:     "tabs collapsed",
			input:    "hello\t\tworld",
			maxLen:   20,
			expected: "hello world",
		},
		{
			name:     "leading and trailing whitespace trimmed",
			input:    "  hello world  ",
			maxLen:   20,
			expected: "hello world",
		},
		{
			name:     "unicode preserved",
			input:    "héllo wörld",
			maxLen:   20,
			expected: "héllo wörld",
		},
		{
			name:     "unicode truncation safe",
			input:    "日本語テスト文字列",
			maxLen:   6,
			expected: "日本語...",
		},
		{
			name:     "emoji handled correctly",
			input:    "hello 👋 world",
			maxLen:   20,
			expected: "hello 👋 world",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
		{
			name:     "whitespace only becomes empty",
			input:    "   \n\t  ",
			maxLen:   10,
			expected: "",
		},
		{
			name:     "complex whitespace normalization with truncation",
			input:    "This is\na multiline\n\ndescription with   extra   spaces",
			maxLen:   30,
			expected: "This is a multiline descrip...",
		},
		{
			name:     "maxLen less than MinTruncateLen clamped to 4",
			input:    "hello",
			maxLen:   2,
			expected: "h...",
		},
		{
			name:     "maxLen of 0 clamped to MinTruncateLen",
			input:    "hello",
			maxLen:   0,
			expected: "h...",
		},
		{
			name:     "negative maxLen clamped to MinTruncateLen",
			input:    "hello",
			maxLen:   -5,
			expected: "h...",
		},
		{
			name:     "maxLen exactly at MinTruncateLen",
			input:    "hello",
			maxLen:   4,
			expected: "h...",
		},
		{
			name:     "short string with small maxLen unchanged",
			input:    "hi",
			maxLen:   3,
			expected: "hi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateDescription(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("TruncateDescription(%q, %d) = %q, want %q",
					tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestTruncateDescription_RuneLength(t *testing.T) {
	// Verify that truncation respects rune count, not byte count
	input := "日本語テスト" // 6 characters, but 18 bytes in UTF-8
	result := TruncateDescription(input, 5)

	// Should truncate to 2 runes + "..." = 5 runes total
	expected := "日本..."
	if result != expected {
		t.Errorf("Expected %q but got %q", expected, result)
	}

	// Verify the result is valid UTF-8 by checking rune count
	runeCount := 0
	for range result {
		runeCount++
	}
	if runeCount != 5 {
		t.Errorf("Expected 5 runes but got %d", runeCount)
	}
}
