package tagstest

import "fmt"

// Numbered returns n distinct valid tags: tag-0, tag-1, …
func Numbered(n int) []string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	return tags
}

// Arg returns tags as a tool argument value.
func Arg(tags []string) []interface{} {
	arg := make([]interface{}, len(tags))
	for i, tag := range tags {
		arg[i] = tag
	}
	return arg
}
