package workflow

import (
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
)

// fieldTags is the workflow tools' tags argument.
const fieldTags = "tags"

// tagsArg declares the tags argument of workflow_create, _update and
// _validate.
var tagsArg = api.ArgMetadata{
	Name:        fieldTags,
	Type:        api.ArgTypeArray,
	Required:    false,
	Description: api.TagsArgDescription,
	Schema: map[string]interface{}{
		api.SchemaKeyType:  string(api.ArgTypeArray),
		api.SchemaKeyItems: map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString)},
	},
}

// convertTags reads and validates a tags argument. nil (omitted) yields no
// tags.
func convertTags(raw interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("must be an array of strings")
	}
	tags := make([]string, 0, len(list))
	for _, item := range list {
		tag, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("must be an array of strings")
		}
		tags = append(tags, tag)
	}
	if err := api.ValidateTags(tags); err != nil {
		return nil, err
	}
	return tags, nil
}
