package api

import "sync"

// RedactedValue replaces a sensitive value in a record muster keeps.
const RedactedValue = "[redacted]"

// sensitiveToolArgs maps an exposed tool name to its sensitive arguments
// (ArgMetadata.Sensitive).
var sensitiveToolArgs sync.Map

// RegisterSensitiveToolArgs records which arguments of an exposed tool are
// sensitive. The aggregator registers the core tools as it exposes them.
func RegisterSensitiveToolArgs(tool string, args []ArgMetadata) {
	var names []string
	for _, arg := range args {
		if arg.Sensitive {
			names = append(names, arg.Name)
		}
	}
	if len(names) == 0 {
		sensitiveToolArgs.Delete(tool)
		return
	}
	sensitiveToolArgs.Store(tool, names)
}

// SensitiveToolArgs returns the sensitive arguments of an exposed tool.
func SensitiveToolArgs(tool string) []string {
	names, _ := sensitiveToolArgs.Load(tool)
	list, _ := names.([]string)
	return list
}
