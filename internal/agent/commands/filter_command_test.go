package commands

import (
	"testing"

	"github.com/giantswarm/muster/internal/metatools"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterCommand_buildFilterArgs(t *testing.T) {
	f := &FilterCommand{}

	t.Run("bare token becomes pattern", func(t *testing.T) {
		args, detailed, err := f.buildFilterArgs([]string{"core_*"})
		require.NoError(t, err)
		assert.False(t, detailed)
		assert.Equal(t, "core_*", args["pattern"])
	})

	t.Run("key=value options are parsed", func(t *testing.T) {
		args, detailed, err := f.buildFilterArgs([]string{
			"query=deploy app", "limit=5", "detailed=true", "case=true",
		})
		require.NoError(t, err)
		assert.True(t, detailed)
		assert.Equal(t, "deploy app", args["query"])
		assert.Equal(t, 5, args["limit"])
		assert.Equal(t, true, args["include_schema"])
		assert.Equal(t, true, args["case_sensitive"])
	})

	t.Run("unknown option fails loud", func(t *testing.T) {
		_, _, err := f.buildFilterArgs([]string{"descripton=deploy"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown filter option")
	})

	t.Run("extra bare token fails loud", func(t *testing.T) {
		_, _, err := f.buildFilterArgs([]string{"core_*", "deploy"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected argument")
	})
}

func TestNameColumnWidth(t *testing.T) {
	assert.Equal(t, 0, nameColumnWidth(nil))
	assert.Equal(t, 6, nameColumnWidth([]metatools.ToolInfo{{Name: "core_a"}, {Name: "x_b"}}))

	long := make([]byte, 80)
	for i := range long {
		long[i] = 'a'
	}
	assert.Equal(t, 50, nameColumnWidth([]metatools.ToolInfo{{Name: string(long)}}), "width is capped")
}

func TestFilterCommand_Completions(t *testing.T) {
	f := &FilterCommand{}

	first := f.Completions("filter")
	assert.Equal(t, []string{"tools"}, first)

	all := f.Completions("filter tools ")
	assert.Contains(t, all, "query=")
	assert.Contains(t, all, "limit=")

	// Already-supplied keys are dropped from completions.
	remaining := f.Completions("filter tools query=deploy ")
	assert.NotContains(t, remaining, "query=")
	assert.Contains(t, remaining, "limit=")
}
