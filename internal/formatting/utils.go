package formatting

import (
	"encoding/json"
	"fmt"
)

// PrettyJSON formats any value as indented JSON for human-readable display.
// It handles marshaling errors gracefully by falling back to fmt.Sprintf.
// This is the consolidated implementation used across all muster packages.
//
// Parameters:
//   - v: The value to format as JSON (any type)
//
// Returns:
//   - string: Formatted JSON with 2-space indentation, or string representation on error
//
// Example:
//
//	data := map[string]interface{}{"name": "test", "value": 42}
//	fmt.Println(formatting.PrettyJSON(data))
//	// Output:
//	// {
//	//   "name": "test",
//	//   "value": 42
//	// }
func PrettyJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
} 