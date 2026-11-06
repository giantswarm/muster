package toolset

import "strings"

// FromScope picks the selectors out of the scope tokens of a token's scope
// claim (RFC 8693 §4.2): the scope tokens of the form kind:name whose kind is
// a selector kind, the reserved ones included so ParseInline refuses them.
// Other scope tokens, such as openid or email, are not selectors and are
// skipped.
func FromScope(scopes []string) []string {
	var selectors []string
	for _, token := range scopes {
		kind, _, ok := strings.Cut(token, ":")
		if !ok {
			continue
		}
		switch kind {
		case KindPreset, KindServer, KindWorkflow, KindTool, kindToolset, kindLabel:
			selectors = append(selectors, token)
		}
	}
	return selectors
}
