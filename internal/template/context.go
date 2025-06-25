package template

// MergeContexts merges multiple contexts into a single context
// Later contexts override values from earlier contexts
func MergeContexts(contexts ...map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for _, ctx := range contexts {
		for key, value := range ctx {
			result[key] = value
		}
	}

	return result
}
