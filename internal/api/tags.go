package api

import (
	"fmt"
	"regexp"
	"strings"
)

// TagsAnnotation holds the free-form tags of an MCPServer or Workflow,
// comma-joined (e.g. "triage,kubernetes"). The key and the tag form are shared
// with the developer portal and the other Agent Platform resources (agents,
// skills), whose frontend parses them with parseTags in ui-react.
const TagsAnnotation = "ui.giantswarm.io/tags"

// TagsArgDescription describes the tags argument of the MCPServer and
// Workflow mutation tools.
const TagsArgDescription = "Tags (ui.giantswarm.io/tags): a-z0-9 words joined by single hyphens, max 63 chars, unique. On update: omitted keeps, [] clears, a list replaces"

// MaxTagLength is the longest tag accepted, in characters.
const MaxTagLength = 63

var tagPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func checkTag(tag string) error {
	if len(tag) > MaxTagLength {
		return fmt.Errorf("tag %q is longer than %d characters", tag, MaxTagLength)
	}
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("tag %q must be lowercase letters and digits, optionally joined by single hyphens", tag)
	}
	return nil
}

// ValidateTags checks tags as a writer receives them: each matches
// ^[a-z0-9]+(-[a-z0-9]+)*$, is at most MaxTagLength characters, and appears
// once. It does not normalise; the caller sends tags in their stored form.
func ValidateTags(tags []string) error {
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if err := checkTag(tag); err != nil {
			return err
		}
		if _, dup := seen[tag]; dup {
			return fmt.Errorf("duplicate tag %q", tag)
		}
		seen[tag] = struct{}{}
	}
	return nil
}

// ParseTags reads a TagsAnnotation value: it splits on commas, trims and
// lowercases each entry, and drops entries that are not valid tags and
// repeats. It never fails, so a malformed annotation set by hand does not
// break a read. It returns nil when no valid tag remains.
func ParseTags(raw string) []string {
	var tags []string
	seen := map[string]struct{}{}
	for _, entry := range strings.Split(raw, ",") {
		tag := strings.ToLower(strings.TrimSpace(entry))
		if checkTag(tag) != nil {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

// ApplyTagsAnnotation applies a write's tags to annotations and returns the
// result, allocating the map when needed. Replace semantics: nil keeps the
// annotation, an empty list removes it, and any other list replaces it,
// comma-joined in order. Callers validate tags with ValidateTags first.
func ApplyTagsAnnotation(annotations map[string]string, tags *[]string) map[string]string {
	switch {
	case tags == nil:
		return annotations
	case len(*tags) == 0:
		delete(annotations, TagsAnnotation)
		return annotations
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[TagsAnnotation] = strings.Join(*tags, ",")
	return annotations
}
