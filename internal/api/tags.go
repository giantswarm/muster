package api

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// TagsAnnotation holds the free-form tags of an MCPServer or Workflow,
// comma-joined (e.g. "triage,kubernetes"). The key and the tag form are shared
// with the developer portal and the other Agent Platform resources (agents,
// skills), whose frontend parses them with parseTags in ui-react.
const TagsAnnotation = "ui.giantswarm.io/tags"

// TagsArgDescription describes the tags argument of the MCPServer and
// Workflow create and validate tools.
const TagsArgDescription = "Up to 50 unique tags (ui.giantswarm.io/tags): a-z0-9 words joined by single hyphens, max 63 chars"

// TagsUpdateArgDescription describes the tags argument of the MCPServer and
// Workflow update tools.
const TagsUpdateArgDescription = "Replaces the tags; omitted keeps, [] clears. Unique a-z0-9 words joined by single hyphens, max 63 chars; at most 50, unless none is new"

// TagPattern is the form of a single tag.
const TagPattern = `^[a-z0-9]+(-[a-z0-9]+)*$`

// MaxTagLength is the longest tag accepted, in characters.
const MaxTagLength = 63

// MaxTags is the most tags a write accepts, as for agents and skills in the
// developer portal.
const MaxTags = 50

var tagPattern = regexp.MustCompile(TagPattern)

func checkTag(tag string) error {
	if len(tag) > MaxTagLength {
		return fmt.Errorf("tag %q is longer than %d characters", tag, MaxTagLength)
	}
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("tag %q must be lowercase letters and digits, optionally joined by single hyphens", tag)
	}
	return nil
}

// ValidateTags checks tags as a create receives them: each matches
// ^[a-z0-9]+(-[a-z0-9]+)*$, is at most MaxTagLength characters, and appears
// once, and there are at most MaxTags of them. It does not normalise; the
// caller sends tags in their stored form.
func ValidateTags(tags []string) error {
	return ValidateTagsUpdate(tags, nil)
}

// ValidateTagsUpdate checks the tags an update sends against the resource's
// stored ones (ParseTags of its annotation): as ValidateTags, except that more
// than MaxTags pass when none of them is new. A list set past the limit by
// hand or GitOps can be sent back unchanged or shortened; only adding to it is
// refused, as in the developer portal.
func ValidateTagsUpdate(tags, stored []string) error {
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
	isNew := func(tag string) bool { return !slices.Contains(stored, tag) }
	if len(tags) > MaxTags && slices.ContainsFunc(tags, isNew) {
		return fmt.Errorf("at most %d tags", MaxTags)
	}
	return nil
}

// InvalidTagsError is a write's tags refused by ValidateTags or
// ValidateTagsUpdate.
type InvalidTagsError struct{ Err error }

func (e *InvalidTagsError) Error() string { return "invalid tags: " + e.Err.Error() }

func (e *InvalidTagsError) Unwrap() error { return e.Err }

// TagsArg declares the tags argument of the MCPServer and Workflow create and
// validate tools, with ValidateTags' rules in its JSON Schema. A fresh value
// per call, so tools share no schema map.
func TagsArg() ArgMetadata {
	arg := TagsUpdateArg()
	arg.Description = TagsArgDescription
	arg.Schema[SchemaKeyMaxItems] = MaxTags
	return arg
}

// TagsUpdateArg declares the tags argument of the MCPServer and Workflow
// update tools. Its schema leaves out maxItems: ValidateTagsUpdate lets an
// update send back more than MaxTags stored tags.
func TagsUpdateArg() ArgMetadata {
	return ArgMetadata{
		Name:        "tags",
		Type:        ArgTypeArray,
		Description: TagsUpdateArgDescription,
		Schema: map[string]interface{}{
			SchemaKeyType:        string(ArgTypeArray),
			SchemaKeyUniqueItems: true,
			SchemaKeyItems: map[string]interface{}{
				SchemaKeyType:      string(ArgTypeString),
				SchemaKeyPattern:   TagPattern,
				SchemaKeyMaxLength: MaxTagLength,
			},
		},
	}
}

// InvalidTagsResult refuses a create's or validate's tags that fail
// ValidateTags with a tool error. It returns nil when tags is nil (omitted)
// or valid.
func InvalidTagsResult(tags *[]string) *CallToolResult {
	if tags == nil {
		return nil
	}
	if err := ValidateTags(*tags); err != nil {
		return InvalidTagsErrorResult(&InvalidTagsError{Err: err})
	}
	return nil
}

// InvalidTagsUpdateResult refuses an update's tags early, before the stored
// tags are read, when they fail ValidateTagsUpdate whatever is stored: a
// malformed or repeated tag. The limit needs the stored tags and is left to
// ApplyTagsUpdate. It returns nil when tags is nil (omitted) or passes.
func InvalidTagsUpdateResult(tags *[]string) *CallToolResult {
	if tags == nil {
		return nil
	}
	// Against itself as the stored list, no tag is new, so only the format
	// and uniqueness rules apply.
	if err := ValidateTagsUpdate(*tags, *tags); err != nil {
		return InvalidTagsErrorResult(&InvalidTagsError{Err: err})
	}
	return nil
}

// InvalidTagsErrorResult is the tool error for an InvalidTagsError in err's
// chain, as ApplyTagsUpdate returns it, and nil for any other error.
func InvalidTagsErrorResult(err error) *CallToolResult {
	var invalid *InvalidTagsError
	if !errors.As(err, &invalid) {
		return nil
	}
	return &CallToolResult{Content: []interface{}{invalid.Error()}, IsError: true}
}

// ParseTags reads a TagsAnnotation value: it splits on commas, trims and
// lowercases each entry, and drops entries that are not valid tags and
// repeats. It does not enforce MaxTags. It never fails, so a malformed
// annotation set by hand does not break a read. It returns nil when no valid tag remains.
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

// ApplyTagsUpdate checks an update's tags with ValidateTagsUpdate against
// the tags stored in annotations and applies them with ApplyTagsAnnotation.
// A refusal is an *InvalidTagsError; annotations are then returned unchanged.
func ApplyTagsUpdate(annotations map[string]string, tags *[]string) (map[string]string, error) {
	if tags == nil {
		return annotations, nil
	}
	if err := ValidateTagsUpdate(*tags, ParseTags(annotations[TagsAnnotation])); err != nil {
		return annotations, &InvalidTagsError{Err: err}
	}
	return ApplyTagsAnnotation(annotations, tags), nil
}

// ApplyTagsAnnotation applies a write's tags to annotations and returns the
// result, allocating the map when needed. Replace semantics: nil keeps the
// annotation, an empty list removes it, and any other list replaces it,
// comma-joined in order. Callers validate tags first: a create with ValidateTags,
// an update through ApplyTagsUpdate.
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
