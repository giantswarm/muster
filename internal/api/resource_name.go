package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// ValidateResourceName rejects entity names that are unsafe to use as a file
// path segment or as a field in a line-oriented log.
//
// The name comes straight from a caller-controlled tool argument. In
// filesystem mode it is joined into a file path, and filepath.Join collapses
// ".." segments, so without this a name like "../../evil" or "/etc/x" writes
// and reads arbitrary *.yaml files outside the config directory. Control
// characters are rejected for the same reason a separator is: a newline in a
// name forges an extra record in the legacy events.log, which
// parseLegacyEventLine then returns as a genuine-looking event.
//
// The name must therefore be a single path segment made of printable runes:
// no path separators, no control characters, and not a "." / ".." reference.
// Character policy beyond that (e.g. DNS-1123) is intentionally left to the
// higher layers and to Kubernetes admission; filesystem mode accepts names
// such as "special-chars-workflow_123" that Kubernetes would reject.
func ValidateResourceName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid name: must not be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid name %q: must not be a path reference", name)
	}
	// Both separators are spelled out on purpose. filepath.Separator and
	// os.PathSeparator are the host's separator ('/' in a Linux build), so
	// using either would let `a\b` through on Linux — and muster ships a
	// windows/amd64 binary that reads the same config directory, where that
	// name is a subdirectory. The check has to be platform-independent even
	// though the code that consumes it is not.
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid name %q: must not contain path separators", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid name %q: must not contain control characters", name)
		}
	}
	// Dead on POSIX after the separator check, but it catches drive-relative
	// names such as "C:foo" on Windows.
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid name %q: must be a single path segment", name)
	}
	return nil
}
