package metatools

import (
	"slices"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
)

// pendingUnder narrows the servers awaiting sign-in to those whose tools the
// name pattern could match once they are listed, sorted by server name. A
// server names its tools under its tool prefix; one without a prefix could
// own any name and is kept.
func pendingUnder(pending []api.ServerAuthInfo, pattern string, caseSensitive bool) []api.ServerAuthInfo {
	var under []api.ServerAuthInfo
	for _, server := range pending {
		if patternMayMatchPrefix(pattern, server.ToolPrefix, caseSensitive) {
			under = append(under, server)
		}
	}
	slices.SortFunc(under, func(a, b api.ServerAuthInfo) int { return strings.Compare(a.Name, b.Name) })
	return under
}

// patternMayMatchPrefix reports whether the glob pattern could match a name
// under prefix — a name no catalogue lists yet, of which only the prefix is
// known. A pattern without a metacharacter is one name, matching when it is
// under the prefix. Otherwise the pattern's literal head, up to its first
// metacharacter, is held against the prefix: a head that contradicts the
// prefix rules the name out; past a consistent head a star matches any tail.
// `?`, a character class and an escape count as a star, which can keep a
// server, never hide one. The empty pattern matches every name.
func patternMayMatchPrefix(pattern, prefix string, caseSensitive bool) bool {
	if pattern == "" {
		return true
	}
	if !caseSensitive {
		pattern, prefix = strings.ToLower(pattern), strings.ToLower(prefix)
	}
	i := strings.IndexAny(pattern, `*?[\`)
	if i < 0 {
		return strings.HasPrefix(pattern, prefix)
	}
	head := pattern[:i]
	if len(head) >= len(prefix) {
		return strings.HasPrefix(head, prefix)
	}
	return strings.HasPrefix(prefix, head)
}
