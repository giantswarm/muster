package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSensitiveArgs(t *testing.T) {
	args := []ArgMetadata{{Name: "name", Type: ArgTypeString}, {Name: "secret", Type: ArgTypeString, Sensitive: true}}
	schema := InputSchemaFromArgs(args)
	assert.Equal(t, true, schema.Properties["secret"].(map[string]any)["writeOnly"])
	assert.NotContains(t, schema.Properties["name"].(map[string]any), "writeOnly")

	RegisterSensitiveToolArgs("core_x", args)
	assert.Equal(t, []string{"secret"}, SensitiveToolArgs("core_x"))
	RegisterSensitiveToolArgs("core_x", args[:1])
	assert.Empty(t, SensitiveToolArgs("core_x"))
}
