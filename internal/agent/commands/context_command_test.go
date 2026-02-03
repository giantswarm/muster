package commands

import (
	"context"
	"testing"

	musterctx "muster/internal/context"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStorageProvider implements StorageProvider for testing.
type mockStorageProvider struct {
	currentContextName string
	currentContextErr  error
	config             *musterctx.ContextConfig
	loadErr            error
	getContextResult   *musterctx.Context
	getContextErr      error
	contextNames       []string
	contextNamesErr    error
	setCurrentErr      error
	setCurrentCalled   string
}

func (m *mockStorageProvider) GetCurrentContextName() (string, error) {
	if m.currentContextErr != nil {
		return "", m.currentContextErr
	}
	return m.currentContextName, nil
}

func (m *mockStorageProvider) Load() (*musterctx.ContextConfig, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.config != nil {
		return m.config, nil
	}
	return &musterctx.ContextConfig{}, nil
}

func (m *mockStorageProvider) GetContext(name string) (*musterctx.Context, error) {
	if m.getContextErr != nil {
		return nil, m.getContextErr
	}
	return m.getContextResult, nil
}

func (m *mockStorageProvider) GetContextNames() ([]string, error) {
	if m.contextNamesErr != nil {
		return nil, m.contextNamesErr
	}
	return m.contextNames, nil
}

func (m *mockStorageProvider) SetCurrentContext(name string) error {
	m.setCurrentCalled = name
	return m.setCurrentErr
}

// mockOutputForContext captures output for testing.
type mockOutputForContext struct {
	lines []string
}

func (m *mockOutputForContext) Output(format string, args ...interface{}) {}
func (m *mockOutputForContext) OutputLine(format string, args ...interface{}) {
	m.lines = append(m.lines, format)
}
func (m *mockOutputForContext) Info(format string, args ...interface{})  {}
func (m *mockOutputForContext) Debug(format string, args ...interface{}) {}
func (m *mockOutputForContext) Error(format string, args ...interface{}) {}
func (m *mockOutputForContext) Success(format string, args ...interface{}) {
	m.lines = append(m.lines, format)
}
func (m *mockOutputForContext) SetVerbose(verbose bool) {}

func TestContextCommand_ShowCurrent_NoContext(t *testing.T) {
	storage := &mockStorageProvider{currentContextName: ""}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{})

	require.NoError(t, err)
	assert.Contains(t, output.lines[0], "No context set")
}

func TestContextCommand_ShowCurrent_WithContext(t *testing.T) {
	storage := &mockStorageProvider{currentContextName: "production"}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{})

	require.NoError(t, err)
	assert.Contains(t, output.lines[0], "Current context: %s")
}

func TestContextCommand_ListContexts_Empty(t *testing.T) {
	storage := &mockStorageProvider{
		config: &musterctx.ContextConfig{Contexts: []musterctx.Context{}},
	}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"list"})

	require.NoError(t, err)
	assert.Contains(t, output.lines[0], "No contexts configured")
}

func TestContextCommand_ListContexts_WithContexts(t *testing.T) {
	storage := &mockStorageProvider{
		config: &musterctx.ContextConfig{
			CurrentContext: "prod",
			Contexts: []musterctx.Context{
				{Name: "dev", Endpoint: "http://localhost:8090"},
				{Name: "prod", Endpoint: "https://muster.example.com"},
			},
		},
	}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"list"})

	require.NoError(t, err)
	assert.Contains(t, output.lines[0], "Available contexts:")
}

func TestContextCommand_ListContexts_LsAlias(t *testing.T) {
	storage := &mockStorageProvider{
		config: &musterctx.ContextConfig{
			Contexts: []musterctx.Context{
				{Name: "dev", Endpoint: "http://localhost:8090"},
			},
		},
	}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"ls"})

	require.NoError(t, err)
	assert.Contains(t, output.lines[0], "Available contexts:")
}

func TestContextCommand_SwitchContext_Success(t *testing.T) {
	var callbackCalled string
	storage := &mockStorageProvider{
		getContextResult: &musterctx.Context{
			Name:     "production",
			Endpoint: "https://muster.example.com",
		},
	}
	output := &mockOutputForContext{}
	callback := func(name string) { callbackCalled = name }
	cmd := NewContextCommandWithStorage(nil, output, nil, callback, storage)

	err := cmd.Execute(context.Background(), []string{"use", "production"})

	require.NoError(t, err)
	assert.Equal(t, "production", storage.setCurrentCalled)
	assert.Equal(t, "production", callbackCalled)
	assert.Contains(t, output.lines[0], "Switched to context")
}

func TestContextCommand_SwitchContext_NotFound(t *testing.T) {
	storage := &mockStorageProvider{
		getContextResult: nil,
		contextNames:     []string{"dev", "staging"},
	}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"use", "production"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "Available contexts: dev, staging")
}

func TestContextCommand_SwitchContext_DirectName(t *testing.T) {
	// Test that passing just the context name works (without "use" subcommand)
	storage := &mockStorageProvider{
		getContextResult: &musterctx.Context{
			Name:     "dev",
			Endpoint: "http://localhost:8090",
		},
	}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"dev"})

	require.NoError(t, err)
	assert.Equal(t, "dev", storage.setCurrentCalled)
}

func TestContextCommand_SwitchContext_SwitchAlias(t *testing.T) {
	storage := &mockStorageProvider{
		getContextResult: &musterctx.Context{
			Name:     "staging",
			Endpoint: "https://staging.example.com",
		},
	}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"switch", "staging"})

	require.NoError(t, err)
	assert.Equal(t, "staging", storage.setCurrentCalled)
}

func TestContextCommand_SwitchContext_MissingName(t *testing.T) {
	storage := &mockStorageProvider{}
	output := &mockOutputForContext{}
	cmd := NewContextCommandWithStorage(nil, output, nil, nil, storage)

	err := cmd.Execute(context.Background(), []string{"use"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage:")
}

func TestContextCommand_Usage(t *testing.T) {
	cmd := NewContextCommand(nil, nil, nil, nil)
	usage := cmd.Usage()

	assert.Contains(t, usage, "context")
	assert.Contains(t, usage, "list")
	assert.Contains(t, usage, "use")
}

func TestContextCommand_Description(t *testing.T) {
	cmd := NewContextCommand(nil, nil, nil, nil)
	desc := cmd.Description()

	assert.Contains(t, desc, "context")
}

func TestContextCommand_Aliases(t *testing.T) {
	cmd := NewContextCommand(nil, nil, nil, nil)
	aliases := cmd.Aliases()

	assert.Contains(t, aliases, "ctx")
}

func TestContextCommand_Completions_Subcommands(t *testing.T) {
	storage := &mockStorageProvider{}
	cmd := NewContextCommandWithStorage(nil, nil, nil, nil, storage)

	completions := cmd.Completions("")

	assert.Contains(t, completions, "list")
	assert.Contains(t, completions, "ls")
	assert.Contains(t, completions, "use")
	assert.Contains(t, completions, "switch")
}

func TestContextCommand_Completions_ContextNames(t *testing.T) {
	storage := &mockStorageProvider{
		contextNames: []string{"dev", "staging", "prod"},
	}
	cmd := NewContextCommandWithStorage(nil, nil, nil, nil, storage)

	// Note: "use prod" has 2 parts when split by Fields, triggering context name completion
	completions := cmd.Completions("use prod")

	assert.Contains(t, completions, "dev")
	assert.Contains(t, completions, "staging")
	assert.Contains(t, completions, "prod")
}

func TestContextCommand_GetContextNames(t *testing.T) {
	storage := &mockStorageProvider{
		contextNames: []string{"alpha", "beta"},
	}
	cmd := NewContextCommandWithStorage(nil, nil, nil, nil, storage)

	names := cmd.GetContextNames()

	assert.Equal(t, []string{"alpha", "beta"}, names)
}

func TestContextCommand_GetContextNames_Error(t *testing.T) {
	storage := &mockStorageProvider{
		contextNamesErr: assert.AnError,
	}
	cmd := NewContextCommandWithStorage(nil, nil, nil, nil, storage)

	names := cmd.GetContextNames()

	assert.Nil(t, names)
}
