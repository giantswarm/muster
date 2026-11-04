package api

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api/tagstest"
)

func TestValidateTags(t *testing.T) {
	tests := []struct {
		name    string
		tags    []string
		wantErr string
	}{
		{name: "none", tags: nil},
		{name: "empty list", tags: []string{}},
		{name: "valid tags", tags: []string{"triage", "kubernetes", "k8s-1-30", "a"}},
		{name: "63 characters", tags: []string{strings.Repeat("a", 63)}},
		{name: "64 characters", tags: []string{strings.Repeat("a", 64)}, wantErr: "longer than 63 characters"},
		{name: "uppercase", tags: []string{"Triage"}, wantErr: `tag "Triage"`},
		{name: "empty tag", tags: []string{""}, wantErr: `tag ""`},
		{name: "surrounding space", tags: []string{" triage"}, wantErr: `tag " triage"`},
		{name: "comma", tags: []string{"a,b"}, wantErr: `tag "a,b"`},
		{name: "leading hyphen", tags: []string{"-triage"}, wantErr: `tag "-triage"`},
		{name: "trailing hyphen", tags: []string{"triage-"}, wantErr: `tag "triage-"`},
		{name: "double hyphen", tags: []string{"tri--age"}, wantErr: `tag "tri--age"`},
		{name: "underscore", tags: []string{"tri_age"}, wantErr: `tag "tri_age"`},
		{name: "50 tags", tags: tagstest.Numbered(MaxTags)},
		{name: "51 tags", tags: tagstest.Numbered(MaxTags + 1), wantErr: "at most 50 tags"},
		{name: "duplicate", tags: []string{"triage", "kubernetes", "triage"}, wantErr: `duplicate tag "triage"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTags(tt.tags)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateTagsUpdate(t *testing.T) {
	stored := tagstest.Numbered(MaxTags + 10)
	tests := []struct {
		name    string
		tags    []string
		stored  []string
		wantErr string
	}{
		{name: "unchanged list past the limit", tags: stored, stored: stored},
		{name: "reordered list past the limit", tags: append([]string{stored[len(stored)-1]}, stored[:len(stored)-1]...), stored: stored},
		{name: "shortened list still past the limit", tags: stored[1:], stored: stored},
		{name: "a new tag on a list past the limit", tags: append(stored[1:], "new"), stored: stored, wantErr: "at most 50 tags"},
		{name: "51 new tags", tags: tagstest.Numbered(MaxTags + 1), wantErr: "at most 50 tags"},
		{name: "50 new tags", tags: tagstest.Numbered(MaxTags)},
		{name: "format still checked past the limit", tags: append(stored[1:], "Bad"), stored: append(stored, "Bad"), wantErr: `tag "Bad"`},
		{name: "duplicate still refused past the limit", tags: append(stored, stored[0]), stored: stored, wantErr: "duplicate tag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTagsUpdate(tt.tags, tt.stored)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestApplyTagsUpdate(t *testing.T) {
	stored := map[string]string{TagsAnnotation: strings.Join(tagstest.Numbered(MaxTags+1), ","), "other": "kept"}
	ptr := func(tags []string) *[]string { return &tags }

	got, err := ApplyTagsUpdate(stored, nil)
	require.NoError(t, err)
	assert.Equal(t, stored, got, "omitted keeps")

	got, err = ApplyTagsUpdate(map[string]string{"other": "kept"}, ptr(tagstest.Numbered(MaxTags)))
	require.NoError(t, err)
	assert.Equal(t, strings.Join(tagstest.Numbered(MaxTags), ","), got[TagsAnnotation])

	echo := tagstest.Numbered(MaxTags + 1)[1:]
	got, err = ApplyTagsUpdate(maps.Clone(stored), ptr(echo))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{TagsAnnotation: strings.Join(echo, ","), "other": "kept"}, got)

	_, err = ApplyTagsUpdate(maps.Clone(stored), ptr(append(echo, "new", "newer")))
	var invalid *InvalidTagsError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "invalid tags: at most 50 tags", err.Error())

	refused := InvalidTagsErrorResult(fmt.Errorf("failed to update workflow: %w", err))
	require.NotNil(t, refused)
	assert.True(t, refused.IsError)
	assert.Equal(t, []interface{}{"invalid tags: at most 50 tags"}, refused.Content)
	assert.Nil(t, InvalidTagsErrorResult(fmt.Errorf("conflict")))
}

func TestParseTags(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "blank", raw: " , ,", want: nil},
		{name: "one tag", raw: "triage", want: []string{"triage"}},
		{name: "comma-joined", raw: "triage,kubernetes", want: []string{"triage", "kubernetes"}},
		{name: "trims and lowercases", raw: " Triage , KUBERNETES ", want: []string{"triage", "kubernetes"}},
		{name: "drops malformed entries", raw: "triage,bad_tag,-x,ok-1," + strings.Repeat("a", 64), want: []string{"triage", "ok-1"}},
		{name: "dedupes after normalising", raw: "triage,Triage, triage", want: []string{"triage"}},
		{name: "keeps more than MaxTags", raw: strings.Join(tagstest.Numbered(MaxTags+1), ","), want: tagstest.Numbered(MaxTags + 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseTags(tt.raw))
		})
	}
}

func TestApplyTagsAnnotation(t *testing.T) {
	ptr := func(tags ...string) *[]string { return &tags }
	other := map[string]string{DisplayNameAnnotation: "Kagi"}

	tests := []struct {
		name        string
		annotations map[string]string
		tags        *[]string
		want        map[string]string
	}{
		{
			name:        "nil keeps existing tags",
			annotations: map[string]string{TagsAnnotation: "triage", DisplayNameAnnotation: "Kagi"},
			tags:        nil,
			want:        map[string]string{TagsAnnotation: "triage", DisplayNameAnnotation: "Kagi"},
		},
		{
			name:        "nil on no annotations stays nil",
			annotations: nil,
			tags:        nil,
			want:        nil,
		},
		{
			name:        "empty list removes the annotation",
			annotations: map[string]string{TagsAnnotation: "triage", DisplayNameAnnotation: "Kagi"},
			tags:        ptr(),
			want:        other,
		},
		{
			name:        "tags replace the annotation, comma-joined in order",
			annotations: map[string]string{TagsAnnotation: "old", DisplayNameAnnotation: "Kagi"},
			tags:        ptr("triage", "kubernetes"),
			want:        map[string]string{TagsAnnotation: "triage,kubernetes", DisplayNameAnnotation: "Kagi"},
		},
		{
			name:        "tags on no annotations",
			annotations: nil,
			tags:        ptr("triage"),
			want:        map[string]string{TagsAnnotation: "triage"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ApplyTagsAnnotation(tt.annotations, tt.tags))
		})
	}
}

func TestTagsArgSchemaCarriesValidateTagsRules(t *testing.T) {
	schema := InputSchemaFromArgs([]ArgMetadata{TagsArg()})

	require.Equal(t, map[string]any{
		"type":        "array",
		"description": TagsArgDescription,
		"maxItems":    MaxTags,
		"uniqueItems": true,
		"items": map[string]any{
			"type":      "string",
			"pattern":   TagPattern,
			"maxLength": MaxTagLength,
		},
	}, schema.Properties["tags"])
	assert.Empty(t, schema.Required)
}

func TestTagsUpdateArgSchemaLeavesOutMaxItems(t *testing.T) {
	schema := InputSchemaFromArgs([]ArgMetadata{TagsUpdateArg()})

	require.Equal(t, map[string]any{
		"type":        "array",
		"description": TagsUpdateArgDescription,
		"uniqueItems": true,
		"items": map[string]any{
			"type":      "string",
			"pattern":   TagPattern,
			"maxLength": MaxTagLength,
		},
	}, schema.Properties["tags"], "an update may send back more than MaxTags stored tags")
	assert.NotContains(t, TagsArgDescription, "omitted", "create and validate have no update semantics")
}

func TestInvalidTagsResult(t *testing.T) {
	ptr := func(tags ...string) *[]string { return &tags }

	assert.Nil(t, InvalidTagsResult(nil))
	assert.Nil(t, InvalidTagsResult(ptr()))
	assert.Nil(t, InvalidTagsResult(ptr("triage")))

	refused := InvalidTagsResult(ptr("Triage"))
	require.NotNil(t, refused)
	assert.True(t, refused.IsError)
	assert.Equal(t, []interface{}{`invalid tags: tag "Triage" must be lowercase letters and digits, optionally joined by single hyphens`}, refused.Content)
}

func TestInvalidTagsUpdateResult(t *testing.T) {
	ptr := func(tags []string) *[]string { return &tags }

	assert.Nil(t, InvalidTagsUpdateResult(nil))
	assert.Nil(t, InvalidTagsUpdateResult(ptr(tagstest.Numbered(MaxTags+1))), "the limit needs the stored tags")

	for _, tags := range [][]string{{"Triage"}, {"triage", "triage"}} {
		refused := InvalidTagsUpdateResult(ptr(tags))
		require.NotNil(t, refused, "%v", tags)
		assert.True(t, refused.IsError)
		assert.Contains(t, refused.Content[0], "invalid tags: ")
	}
}

func TestUpdateRequestTagsTriState(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want *[]string
	}{
		{name: "omitted", args: map[string]interface{}{"name": "triage"}, want: nil},
		{name: "null", args: map[string]interface{}{"name": "triage", "tags": nil}, want: nil},
		{name: "empty list", args: map[string]interface{}{"name": "triage", "tags": []interface{}{}}, want: &[]string{}},
		{name: "list", args: map[string]interface{}{"name": "triage", "tags": []interface{}{"a", "b"}}, want: &[]string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcpServer MCPServerUpdateRequest
			require.NoError(t, ParseRequest(tt.args, &mcpServer))
			assert.Equal(t, tt.want, mcpServer.Tags)

			var workflow WorkflowUpdateRequest
			require.NoError(t, ParseRequest(tt.args, &workflow))
			assert.Equal(t, tt.want, workflow.Tags)
		})
	}
}
