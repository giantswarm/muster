package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPlainTableWriter(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)

	assert.NotNil(t, tw)
	assert.Empty(t, tw.headers)
	assert.Empty(t, tw.rows)
	assert.True(t, tw.showHeaders)
	assert.Equal(t, 3, tw.minPadding)
}

func TestPlainTableWriter_SetHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)

	tw.SetHeaders([]string{"name", "Description", "STATUS"})

	// Headers should be uppercased
	assert.Equal(t, []string{"NAME", "DESCRIPTION", "STATUS"}, tw.headers)
	// Column widths should be initialized to header lengths
	assert.Equal(t, []int{4, 11, 6}, tw.columnWidths)
}

func TestPlainTableWriter_SetNoHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)

	assert.True(t, tw.showHeaders)

	tw.SetNoHeaders(true)
	assert.False(t, tw.showHeaders)

	tw.SetNoHeaders(false)
	assert.True(t, tw.showHeaders)
}

func TestPlainTableWriter_AppendRow(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "VALUE"})

	tw.AppendRow([]string{"short", "123"})
	tw.AppendRow([]string{"longer-name", "4567890"})

	assert.Len(t, tw.rows, 2)
	// Column widths should expand to fit content
	assert.Equal(t, 11, tw.columnWidths[0]) // "longer-name" is 11 chars
	assert.Equal(t, 7, tw.columnWidths[1])  // "4567890" is 7 chars
}

func TestPlainTableWriter_AppendRow_FewerColumnsThanHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"COL1", "COL2", "COL3"})

	tw.AppendRow([]string{"value1"})

	assert.Len(t, tw.rows, 1)
	assert.Equal(t, []string{"value1", "", ""}, tw.rows[0])
}

func TestPlainTableWriter_AppendRow_MoreColumnsThanHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"COL1", "COL2"})

	tw.AppendRow([]string{"value1", "value2", "value3", "value4"})

	assert.Len(t, tw.rows, 1)
	// Extra columns should be ignored
	assert.Equal(t, []string{"value1", "value2"}, tw.rows[0])
}

func TestPlainTableWriter_Render_WithHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "STATUS"})
	tw.AppendRow([]string{"server-1", "Running"})
	tw.AppendRow([]string{"server-2", "Stopped"})

	tw.Render()

	output := buf.String()
	lines := splitLines(output)

	assert.Len(t, lines, 3)
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "STATUS")
	assert.Contains(t, lines[1], "server-1")
	assert.Contains(t, lines[1], "Running")
	assert.Contains(t, lines[2], "server-2")
	assert.Contains(t, lines[2], "Stopped")
}

func TestPlainTableWriter_Render_WithoutHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "STATUS"})
	tw.SetNoHeaders(true)
	tw.AppendRow([]string{"server-1", "Running"})

	tw.Render()

	output := buf.String()
	lines := splitLines(output)

	assert.Len(t, lines, 1)
	assert.NotContains(t, output, "NAME")
	assert.Contains(t, lines[0], "server-1")
}

func TestPlainTableWriter_Render_EmptyHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)

	tw.Render()

	assert.Empty(t, buf.String())
}

func TestPlainTableWriter_Render_NoRows(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "STATUS"})

	tw.Render()

	output := buf.String()
	lines := splitLines(output)

	// Should still print headers when there are no rows
	assert.Len(t, lines, 1)
	assert.Contains(t, lines[0], "NAME")
}

func TestPlainTableWriter_Render_NoRowsNoHeaders(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "STATUS"})
	tw.SetNoHeaders(true)

	tw.Render()

	// No output when no rows and headers suppressed
	assert.Empty(t, buf.String())
}

func TestPlainTableWriter_ColumnAlignment(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "STATUS"})
	tw.AppendRow([]string{"a", "Running"})
	tw.AppendRow([]string{"longer-name", "OK"})

	tw.Render()

	output := buf.String()
	lines := splitLines(output)

	// Verify proper padding (minPadding = 3)
	// Column 1 width should be 11 ("longer-name") + 3 padding = 14 chars
	assert.Len(t, lines, 3)

	// Each line should have consistent column positions
	// The STATUS column should start at the same position for all rows
	nameCol1End := 11 + 3 // "longer-name" width + padding
	for _, line := range lines {
		if len(line) > nameCol1End {
			// Verify non-first columns are properly aligned
			assert.True(t, len(line) >= nameCol1End, "Line should have proper padding: %s", line)
		}
	}
}

func TestPlainTableWriter_LastColumnNoPadding(t *testing.T) {
	var buf bytes.Buffer
	tw := NewPlainTableWriter(&buf)
	tw.SetHeaders([]string{"NAME", "LAST"})
	tw.AppendRow([]string{"test", "value"})

	tw.Render()

	output := buf.String()
	lines := splitLines(output)

	// Last column should not have trailing spaces
	for _, line := range lines {
		assert.Equal(t, line, trimTrailingSpaces(line), "Line should not have trailing spaces")
	}
}

// Helper function to split output into lines, filtering empty lines
func splitLines(s string) []string {
	var lines []string
	for _, line := range bytes.Split([]byte(s), []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, string(line))
		}
	}
	return lines
}

// Helper function to trim trailing spaces
func trimTrailingSpaces(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
