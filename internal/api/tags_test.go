package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{name: "50 tags", tags: numberedTags(MaxTags)},
		{name: "51 tags", tags: numberedTags(MaxTags + 1), wantErr: "at most 50 tags"},
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

// numberedTags returns n distinct valid tags.
func numberedTags(n int) []string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	return tags
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
		{name: "keeps more than MaxTags", raw: strings.Join(numberedTags(MaxTags+1), ","), want: numberedTags(MaxTags + 1)},
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
