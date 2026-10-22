package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// isRowArray reports whether value is a non-empty array whose items are all
// objects, i.e. rows that render as a table.
func isRowArray(value interface{}) bool {
	arr, ok := value.([]interface{})
	if !ok || len(arr) == 0 {
		return false
	}
	for _, item := range arr {
		if _, ok := item.(map[string]interface{}); !ok {
			return false
		}
	}
	return true
}

// writeRowTable prints rows (objects) as a table in their given order: one
// column per key found in any row, sorted alphabetically, and every cell in
// full -- list values joined, nested objects as compact JSON -- so the table
// carries the same data as the JSON output.
func (f *TableFormatter) writeRowTable(w io.Writer, rows []interface{}) {
	keySet := make(map[string]bool)
	for _, row := range rows {
		for key := range row.(map[string]interface{}) {
			keySet[key] = true
		}
	}
	columns := make([]string, 0, len(keySet))
	for key := range keySet {
		columns = append(columns, key)
	}
	sort.Strings(columns)

	tw := NewPlainTableWriter(w)
	tw.SetHeaders(columns)
	tw.SetNoHeaders(f.options.NoHeaders)
	for _, row := range rows {
		rowMap := row.(map[string]interface{})
		cells := make([]string, len(columns))
		for i, col := range columns {
			cells[i] = formatRowCell(rowMap[col])
		}
		tw.AppendRow(cells)
	}
	tw.Render()
}

// formatRowCell renders one row-table cell without losing data: a missing
// value or empty string is "-", a list joins its items with ", ", and an
// object is compact JSON.
func formatRowCell(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return "-"
	case string:
		if v == "" {
			return "-"
		}
		return v
	case []interface{}:
		if len(v) == 0 {
			return "-"
		}
		items := make([]string, len(v))
		for i, item := range v {
			items[i] = formatRowCell(item)
		}
		return strings.Join(items, ", ")
	case map[string]interface{}:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	default:
		return fmt.Sprintf("%v", v)
	}
}
