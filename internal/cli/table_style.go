package cli

import (
	"fmt"
	"io"
	"strings"
)

// PlainTableWriter provides kubectl-style plain table output without box-drawing characters.
// This format is optimized for:
//   - Easy copy/paste operations
//   - Piping to grep, awk, cut and other command-line tools
//   - Terminal-agnostic rendering (no Unicode issues)
//   - Familiar kubectl-like appearance
type PlainTableWriter struct {
	// headers contains the column header names
	headers []string
	// rows contains the table data rows
	rows [][]string
	// columnWidths tracks the maximum width of each column
	columnWidths []int
	// minPadding is the minimum space between columns
	minPadding int
	// showHeaders controls whether to display the header row
	showHeaders bool
	// output is the writer to output to
	output io.Writer
}

// NewPlainTableWriter creates a new plain table writer with kubectl-style formatting.
// By default, headers are shown. Use SetNoHeaders(true) to suppress them.
func NewPlainTableWriter(output io.Writer) *PlainTableWriter {
	return &PlainTableWriter{
		headers:      []string{},
		rows:         [][]string{},
		columnWidths: []int{},
		minPadding:   3,
		showHeaders:  true,
		output:       output,
	}
}

// SetHeaders sets the column headers for the table.
// Headers are displayed in uppercase by default.
func (w *PlainTableWriter) SetHeaders(headers []string) {
	w.headers = make([]string, len(headers))
	w.columnWidths = make([]int, len(headers))
	for i, h := range headers {
		upper := strings.ToUpper(h)
		w.headers[i] = upper
		w.columnWidths[i] = len(upper)
	}
}

// SetNoHeaders controls whether to suppress the header row.
func (w *PlainTableWriter) SetNoHeaders(noHeaders bool) {
	w.showHeaders = !noHeaders
}

// AppendRow adds a row to the table.
func (w *PlainTableWriter) AppendRow(row []string) {
	// Ensure row has same number of columns as headers
	normalizedRow := make([]string, len(w.headers))
	for i := range w.headers {
		if i < len(row) {
			normalizedRow[i] = row[i]
			// Update column width if this cell is wider
			if len(row[i]) > w.columnWidths[i] {
				w.columnWidths[i] = len(row[i])
			}
		} else {
			normalizedRow[i] = ""
		}
	}
	w.rows = append(w.rows, normalizedRow)
}

// Render outputs the table in kubectl-style format.
func (w *PlainTableWriter) Render() {
	if len(w.headers) == 0 {
		return
	}

	// Don't output anything if no rows and headers are suppressed
	if len(w.rows) == 0 && !w.showHeaders {
		return
	}

	// Print headers if enabled
	if w.showHeaders {
		w.printRow(w.headers)
	}

	// Print data rows
	for _, row := range w.rows {
		w.printRow(row)
	}
}

// printRow prints a single row with proper column alignment.
func (w *PlainTableWriter) printRow(row []string) {
	var sb strings.Builder
	for i, cell := range row {
		if i == len(row)-1 {
			// Last column: no padding needed
			sb.WriteString(cell)
		} else {
			// Pad cell to column width plus minimum padding
			format := fmt.Sprintf("%%-%ds", w.columnWidths[i]+w.minPadding)
			sb.WriteString(fmt.Sprintf(format, cell))
		}
	}
	fmt.Fprintln(w.output, strings.TrimRight(sb.String(), " "))
}
