package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

// readMockClient is a session's connection to a per-user server that answers
// resource reads and prompt retrievals.
type readMockClient struct {
	callToolMockClient
	readURIs   []string
	promptName string
}

func (m *readMockClient) ReadResource(_ context.Context, uri string) (*mcp.ReadResourceResult, error) {
	m.readURIs = append(m.readURIs, uri)
	return &mcp.ReadResourceResult{Contents: []mcp.ResourceContents{
		mcp.TextResourceContents{URI: uri, MIMEType: "application/json", Text: `{"events":[]}`},
	}}, nil
}

func (m *readMockClient) GetPrompt(_ context.Context, name string, _ map[string]interface{}) (*mcp.GetPromptResult, error) {
	m.promptName = name
	return &mcp.GetPromptResult{Description: "per-user prompt"}, nil
}

const (
	readServer  = "beekeeper"
	readSession = "session-1"
	readSubject = "alice"
	readURI     = "beekeeper://feed"
	readPrompt  = "brief"
)

// newPerUserReadFixture is an aggregator with one per-user server whose
// resources and prompts the given session's capability entry lists; no
// listing of the session's view has run yet.
func newPerUserReadFixture(t *testing.T, sessionID string) *AggregatorServer {
	t.Helper()
	a := newTestAggregatorWithPool(t)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: readServer, ToolPrefix: readServer},
		URL:                "https://beekeeper.example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
	}))
	require.NoError(t, a.capabilityStore.Set(context.Background(), sessionID, readServer, &oauthstore.Capabilities{
		Resources: []mcp.Resource{{URI: readURI, Name: "feed"}},
		Prompts:   []mcp.Prompt{{Name: readPrompt}},
	}))
	return a
}

func readContext(sessionID string) context.Context {
	return api.WithSubject(api.WithSessionID(context.Background(), sessionID), readSubject)
}

// A signed-in session reads a per-user server's resource through its own
// connection, though it never listed resources and the server has no shared
// client.
func TestReadResource_PerUserServerReadsThroughSessionClient(t *testing.T) {
	a := newPerUserReadFixture(t, readSession)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), readSession, readServer))
	client := &readMockClient{}
	a.connPool.Put(readSession, readServer, client)

	result, err := a.ReadResource(readContext(readSession), readURI, "")

	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	text, ok := result.Contents[0].(mcp.TextResourceContents)
	require.True(t, ok)
	assert.JSONEq(t, `{"events":[]}`, text.Text)
	assert.Equal(t, []string{readURI}, client.readURIs, "read through the session's connection")

	// The explicit server selector takes the same path.
	_, err = a.ReadResource(readContext(readSession), readURI, readServer)
	require.NoError(t, err)
	assert.Len(t, client.readURIs, 2)
}

// A session not signed in to the per-user server is told so, not that the
// server has no active client.
func TestReadResource_PerUserServerWithoutSignInIsAuthRequired(t *testing.T) {
	a := newPerUserReadFixture(t, readSession)
	// Another session's listing recorded the URI; this session holds no
	// grant for the server.
	a.registry.ExposedResourceURI(readServer, readURI)

	_, err := a.ReadResource(readContext("session-2"), readURI, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not authenticated to server "+readServer)
	assert.NotContains(t, err.Error(), "has no active client")
}

// A per-user server's prompt is retrieved through the session's connection
// the same way.
func TestGetPrompt_PerUserServerUsesSessionClient(t *testing.T) {
	a := newPerUserReadFixture(t, readSession)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), readSession, readServer))
	client := &readMockClient{}
	a.connPool.Put(readSession, readServer, client)

	exposed := a.registry.ExposedPromptName(readServer, readPrompt)
	result, err := a.GetPrompt(readContext(readSession), exposed, nil)

	require.NoError(t, err)
	assert.Equal(t, "per-user prompt", result.Description)
	assert.Equal(t, readPrompt, client.promptName)
}
