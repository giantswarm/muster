package cmd

import (
	"encoding/json"
	"strings"
)

// hasJSONLikeValue reports whether any parameter is a string that starts like
// a JSON array or object, the only values decodeJSONArgs may change.
func hasJSONLikeValue(params map[string]interface{}) bool {
	for _, value := range params {
		if s, ok := value.(string); ok && jsonContainerKind(s) != "" {
			return true
		}
	}
	return false
}

// decodeJSONArgs replaces each string parameter that parses as a JSON array
// or object with that array or object, when the tool's input schema
// properties type the parameter "array" or "object" respectively. Every other
// parameter keeps the value the command line spelled, so a string argument
// that looks like JSON is passed unchanged.
func decodeJSONArgs(params map[string]interface{}, properties map[string]interface{}) {
	for name, value := range params {
		s, ok := value.(string)
		if !ok {
			continue
		}
		kind := jsonContainerKind(s)
		if kind == "" || !schemaAllowsType(properties[name], kind) {
			continue
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			continue
		}
		params[name] = decoded
	}
}

// jsonContainerKind returns "array" or "object" for a value that starts like
// that JSON container, "" otherwise.
func jsonContainerKind(s string) string {
	switch t := strings.TrimSpace(s); {
	case strings.HasPrefix(t, "["):
		return "array"
	case strings.HasPrefix(t, "{"):
		return "object"
	default:
		return ""
	}
}

// schemaAllowsType reports whether a JSON Schema property declares kind as
// its type, given as a single type or a list of types.
func schemaAllowsType(property interface{}, kind string) bool {
	prop, ok := property.(map[string]interface{})
	if !ok {
		return false
	}
	switch t := prop["type"].(type) {
	case string:
		return t == kind
	case []interface{}:
		for _, item := range t {
			if item == kind {
				return true
			}
		}
	}
	return false
}
