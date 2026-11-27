package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// dexSubject has the shape of a Dex subject: the raw URL-safe base64 of the
// user ID and connector ID.
const dexSubject = "CgcxMjM0NTY3EgZnaXRodWI"

// recordingProviders hands out a static credential per source identity and
// records which source identities it was asked for, in order.
type recordingProviders struct {
	mu        sync.Mutex
	requested []string
}

func (r *recordingProviders) newProvider(sourceIdentity string) aws.CredentialsProvider {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requested = append(r.requested, sourceIdentity)
	return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "AKID-" + sourceIdentity, SecretAccessKey: "secret"}, nil
	})
}

func (r *recordingProviders) sourceIdentities() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requested...)
}

func TestSigV4SourceIdentity(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		want    string
		wantErr bool
	}{
		{name: "no caller is muster itself", subject: "", want: "muster-aggregator"},
		{name: "a Dex subject", subject: dexSubject, want: dexSubject},
		{name: "an email", subject: "jane.doe@example.com", want: "jane.doe@example.com"},
		{name: "the longest STS accepts", subject: strings.Repeat("a", 256), want: strings.Repeat("a", 256)},
		{name: "too short", subject: "a", wantErr: true},
		{name: "too long", subject: strings.Repeat("a", 257), wantErr: true},
		{name: "a space", subject: "jane doe", wantErr: true},
		{name: "the aws: prefix", subject: "aws:admin", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sigV4SourceIdentity(tt.subject)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSourceIdentityCredentialsAssumesOncePerCaller(t *testing.T) {
	providers := &recordingProviders{}
	creds := newSourceIdentityCredentials(providers.newProvider, time.Hour)

	for _, subject := range []string{dexSubject, "", dexSubject, "jane.doe@example.com", ""} {
		got, err := creds.Retrieve(api.WithSubject(t.Context(), subject))
		require.NoError(t, err)
		want, err := sigV4SourceIdentity(subject)
		require.NoError(t, err)
		require.Equal(t, "AKID-"+want, got.AccessKeyID)
	}

	require.Equal(t, []string{dexSubject, "muster-aggregator", "jane.doe@example.com"}, providers.sourceIdentities())
}

func TestSourceIdentityCredentialsRefusesASubjectSTSWouldRefuse(t *testing.T) {
	providers := &recordingProviders{}
	creds := newSourceIdentityCredentials(providers.newProvider, time.Hour)

	_, err := creds.Retrieve(api.WithSubject(t.Context(), "jane doe"))
	require.ErrorContains(t, err, "source identity")
	require.Empty(t, providers.sourceIdentities())
}

func TestSourceIdentityCredentialsForgetsIdleCallers(t *testing.T) {
	providers := &recordingProviders{}
	creds := newSourceIdentityCredentials(providers.newProvider, time.Hour)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	creds.now = func() time.Time { return now }

	_, err := creds.Retrieve(api.WithSubject(t.Context(), dexSubject))
	require.NoError(t, err)

	now = now.Add(2 * time.Hour)
	_, err = creds.Retrieve(api.WithSubject(t.Context(), "jane.doe@example.com"))
	require.NoError(t, err)
	require.Len(t, creds.providers, 1)

	_, err = creds.Retrieve(api.WithSubject(t.Context(), dexSubject))
	require.NoError(t, err)
	require.Equal(t, []string{dexSubject, "jane.doe@example.com", dexSubject}, providers.sourceIdentities())
}

// TestSigV4ClientSignsEachCallAsItsCaller drives a real MCP exchange through
// the signing transport: the connect requests carry no caller and sign as
// muster, and a tool call signs with the session of the subject on its
// context.
func TestSigV4ClientSignsEachCallAsItsCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	backend := server.NewMCPServer("aws", "test", server.WithToolCapabilities(false))
	backend.AddTool(mcp.NewTool("whoami"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	var (
		mu       sync.Mutex
		accessKs []string
	)
	mcpHandler := server.NewStreamableHTTPServer(backend)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			auth := r.Header.Get("Authorization")
			start := strings.Index(auth, "Credential=") + len("Credential=")
			mu.Lock()
			accessKs = append(accessKs, auth[start:start+strings.Index(auth[start:], "/")])
			mu.Unlock()
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	providers := &recordingProviders{}
	client := &StreamableHTTPClient{
		url: ts.URL,
		httpClientFunc: func(context.Context) (*http.Client, error) {
			return &http.Client{Transport: &sigV4Transport{
				base:    http.DefaultTransport,
				signer:  v4.NewSigner(),
				creds:   newSourceIdentityCredentials(providers.newProvider, time.Hour),
				region:  testSigV4Region,
				service: "aws-mcp",
			}}, nil
		},
	}
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.Initialize(ctx))
	_, err := client.CallTool(api.WithSubject(ctx, dexSubject), "whoami", nil)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, accessKs, "AKID-muster-aggregator")
	require.Equal(t, "AKID-"+dexSubject, accessKs[len(accessKs)-1])
	require.Equal(t, []string{"muster-aggregator", dexSubject}, providers.sourceIdentities())
}
