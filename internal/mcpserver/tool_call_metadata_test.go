package mcpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const (
	testSessionKey = "com.example/session"
	testOtherKey   = "com.example/other"
)

// metadataBackend serves a tool "probe" over streamable HTTP and records the
// `_meta` entries and the client capabilities.extensions of each call.
// Protocol keys (io.modelcontextprotocol/...) are left out of the record.
type metadataBackend struct {
	url      string
	meta     atomic.Pointer[map[string]any]
	declared atomic.Pointer[map[string]any]
}

func newMetadataBackend(t *testing.T, extensions map[string]any, versions ...string) *metadataBackend {
	t.Helper()
	b := &metadataBackend{}
	var opts []server.ServerOption
	if extensions != nil {
		opts = append(opts, server.WithExtensions(extensions))
	}
	srv := server.NewMCPServer("metadata-backend", "test", append(opts, server.WithToolCapabilities(true))...)
	srv.AddTool(mcp.NewTool("probe"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		meta := map[string]any{}
		if req.Params.Meta != nil {
			for key, value := range req.Params.Meta.AdditionalFields {
				if !strings.HasPrefix(key, "io.modelcontextprotocol/") {
					meta[key] = value
				}
			}
		}
		b.meta.Store(&meta)
		declared := declaredCapabilities(ctx).Extensions
		b.declared.Store(&declared)
		return mcp.NewToolResultText("ok"), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithStreamableHTTPProtocolVersions(versions...)))
	t.Cleanup(ts.Close)
	b.url = ts.URL
	return b
}

func accepting(keys ...any) map[string]any {
	return map[string]any{api.ToolCallMetadataExtension: map[string]any{"keys": keys}}
}

func callerValues() map[string]any {
	return map[string]any{testSessionKey: "a1", testOtherKey: map[string]any{"format": "csv"}}
}

// revisions maps each revision under test to the versions its backend serves.
var revisions = map[string][]string{
	mcp.ProtocolVersion20260728:        nil,
	mcp.LATEST_LEGACY_PROTOCOL_VERSION: {mcp.LATEST_LEGACY_PROTOCOL_VERSION},
}

func probe(t *testing.T, ctx context.Context, c MCPClient) {
	t.Helper()
	_, err := c.CallTool(ctx, "probe", nil)
	require.NoError(t, err)
}

func TestToolCallMetadata_OnlyTheKeysABackendAccepts(t *testing.T) {
	for name, versions := range revisions {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			session := newMetadataBackend(t, accepting(testSessionKey, "com.example/unsent"), versions...)
			silent := newMetadataBackend(t, nil, versions...)
			sameID := newMetadataBackend(t, map[string]any{testSessionKey: map[string]any{}}, versions...)
			malformed := newMetadataBackend(t, map[string]any{api.ToolCallMetadataExtension: map[string]any{"keys": testSessionKey}}, versions...)
			clients := map[string]*StreamableHTTPClient{}
			for key, b := range map[string]*metadataBackend{"session": session, "silent": silent, "same-id": sameID, "malformed": malformed} {
				clients[key] = connect(t, ctx, b.url)
				require.Equal(t, name, clients[key].NegotiatedProtocolVersion())
				probe(t, WithToolCallMetadata(ctx, "alice", key, callerValues()), clients[key])
			}

			require.Equal(t, map[string]any{api.ToolCallMetadataExtension: map[string]any{testSessionKey: "a1"}}, *session.meta.Load(),
				"exactly the accepted keys, under the extension's entry")
			require.Empty(t, *silent.meta.Load(), "a backend that does not declare the extension gets nothing")
			require.Empty(t, *sameID.meta.Load(), "declaring an extension named like a key is not accepting the key")
			require.Empty(t, *malformed.meta.Load(), "settings without a keys array accept nothing")
		})
	}
}

func TestToolCallMetadata_NothingWithoutCallerValues(t *testing.T) {
	for name, versions := range revisions {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			backend := newMetadataBackend(t, accepting(testSessionKey), versions...)

			probe(t, ctx, connect(t, ctx, backend.url))

			require.Empty(t, *backend.meta.Load())
		})
	}
}

func TestToolCallMetadata_MusterDeclaresTheExtension(t *testing.T) {
	for name, versions := range revisions {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			backend := newMetadataBackend(t, accepting(testSessionKey), versions...)
			silent := newMetadataBackend(t, nil, versions...)

			probe(t, ctx, connect(t, ctx, backend.url))
			probe(t, ctx, connect(t, ctx, silent.url))

			want := map[string]any{api.ToolCallMetadataExtension: map[string]any{}}
			require.Equal(t, want, *backend.declared.Load())
			require.Equal(t, want, *silent.declared.Load(), "declared to every backend, whatever it accepts")
		})
	}
}

func TestToolCallMetadata_AnEntryOnTheCallIsKept(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	set := &mcp.Meta{AdditionalFields: map[string]any{api.ToolCallMetadataExtension: map[string]any{testSessionKey: "set"}}}
	got := withToolCallMetadata(WithToolCallMetadata(ctx, "alice", "backend", callerValues()), set, []string{testSessionKey})
	require.Equal(t, map[string]any{testSessionKey: "set"}, got.AdditionalFields[api.ToolCallMetadataExtension])
}

func TestAcceptedToolCallMetadataKeys(t *testing.T) {
	require.Nil(t, AcceptedToolCallMetadataKeys(mcp.ServerCapabilities{}))
	require.Equal(t, []string{"com.example/a", "com.example/b"}, AcceptedToolCallMetadataKeys(mcp.ServerCapabilities{
		Extensions: accepting("com.example/b", "com.example/a", "com.example/a", "tenant", "io.modelcontextprotocol/ui", 7),
	}), "sorted and unique; invalid and reserved keys are dropped")
	require.Nil(t, AcceptedToolCallMetadataKeys(mcp.ServerCapabilities{
		Experimental: accepting("com.example/a"),
	}), "experimental capabilities are not read")
}
