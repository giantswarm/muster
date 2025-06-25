package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSimpleValidationIntegration tests basic validation integration
func TestSimpleValidationIntegration(t *testing.T) {
	// Setup temporary directories with project context
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	// Test capabilities validation integration
	t.Run("Capabilities_ValidationIntegration", func(t *testing.T) {
		testDir := filepath.Join(musterDir, "capabilities")
		require.NoError(t, os.MkdirAll(testDir, 0755))

		// Create valid capability
		validCap := map[string]interface{}{
			"name":        "test-capability",
			"type":        "test_capability",
			"description": "Test capability",
			"operations": map[string]interface{}{
				"test_op": map[string]interface{}{
					"description": "Test operation",
					"requires":    []string{"test_tool"},
				},
			},
		}
		validData, _ := yaml.Marshal(validCap)
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "valid.yaml"), validData, 0644))

		// Create invalid capability (missing operations)
		invalidCap := map[string]interface{}{
			"name":        "invalid-capability",
			"type":        "test_capability",
			"description": "Invalid capability",
			// Missing operations
		}
		invalidData, _ := yaml.Marshal(invalidCap)
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "invalid.yaml"), invalidData, 0644))

		// Test with simple validator
		validator := func(item map[string]interface{}) error {
			if name, exists := item["name"]; !exists || name == "" {
				return fmt.Errorf("name is required")
			}
			if operations, exists := item["operations"]; !exists || operations == nil {
				return fmt.Errorf("operations are required")
			}
			return nil
		}

		results, errors, err := LoadAndParseYAML[map[string]interface{}]("capabilities", validator)

		require.NoError(t, err)
		assert.Equal(t, 1, len(results), "Should load 1 valid capability")
		assert.Equal(t, 1, errors.Count(), "Should have 1 validation error")

		// Verify the valid capability was loaded
		assert.Equal(t, "test-capability", results[0]["name"])
	})

	// Test serviceclasses validation integration
	t.Run("ServiceClasses_ValidationIntegration", func(t *testing.T) {
		testDir := filepath.Join(musterDir, "serviceclasses")
		require.NoError(t, os.MkdirAll(testDir, 0755))

		// Create valid serviceclass
		validSC := map[string]interface{}{
			"name":    "test-serviceclass",
			"type":    "test_service",
			"version": "1.0.0",
			"serviceConfig": map[string]interface{}{
				"serviceType": "TestService",
				"lifecycleTools": map[string]interface{}{
					"start": map[string]interface{}{
						"tool": "test_start_tool",
					},
					"stop": map[string]interface{}{
						"tool": "test_stop_tool",
					},
				},
			},
		}
		validData, _ := yaml.Marshal(validSC)
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "valid.yaml"), validData, 0644))

		// Create invalid serviceclass (missing version)
		invalidSC := map[string]interface{}{
			"name": "invalid-serviceclass",
			"type": "test_service",
			// Missing version
			"serviceConfig": map[string]interface{}{
				"serviceType": "TestService",
			},
		}
		invalidData, _ := yaml.Marshal(invalidSC)
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "invalid.yaml"), invalidData, 0644))

		// Test with simple validator
		validator := func(item map[string]interface{}) error {
			if name, exists := item["name"]; !exists || name == "" {
				return fmt.Errorf("name is required")
			}
			if version, exists := item["version"]; !exists || version == "" {
				return fmt.Errorf("version is required")
			}
			return nil
		}

		results, errors, err := LoadAndParseYAML[map[string]interface{}]("serviceclasses", validator)

		require.NoError(t, err)
		assert.Equal(t, 1, len(results), "Should load 1 valid serviceclass")
		assert.Equal(t, 1, errors.Count(), "Should have 1 validation error")

		// Verify the valid serviceclass was loaded
		assert.Equal(t, "test-serviceclass", results[0]["name"])
	})
}

// TestMalformedYAMLHandling tests that malformed YAML is handled gracefully
func TestMalformedYAMLHandling(t *testing.T) {
	// Setup temporary directories with project context
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	testDir := filepath.Join(musterDir, "workflows")
	require.NoError(t, os.MkdirAll(testDir, 0755))

	// Create valid workflow
	validWF := map[string]interface{}{
		"name":        "test-workflow",
		"description": "Test workflow",
		"steps": []map[string]interface{}{
			{
				"id":   "test_step",
				"tool": "test_tool",
				"args": map[string]interface{}{
					"param": "value",
				},
			},
		},
	}
	validData, _ := yaml.Marshal(validWF)
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "valid.yaml"), validData, 0644))

	// Create malformed YAML
	malformedYAML := []byte(`
name: malformed-workflow
description: This has malformed YAML
steps: [
  - id: test
    tool: test_tool
    args: {
      unclosed: [
`)
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "malformed.yaml"), malformedYAML, 0644))

	// Test with no validator (just YAML parsing)
	results, errors, err := LoadAndParseYAML[map[string]interface{}]("workflows", nil)

	require.NoError(t, err)
	assert.Equal(t, 1, len(results), "Should load 1 valid workflow")
	assert.Equal(t, 1, errors.Count(), "Should have 1 parse error")

	// Verify the valid workflow was loaded
	assert.Equal(t, "test-workflow", results[0]["name"])

	// Verify the error is a parse error
	errorList := errors.Errors
	assert.Equal(t, "parse", errorList[0].ErrorType)
	assert.Contains(t, errorList[0].Message, "Invalid YAML format")
}
