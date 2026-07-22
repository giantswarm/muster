package aggregator

import (
	"context"
	"net/http"
	"testing"
)

// fakeOAuthServer implements the oauthServer interface for testing the
// aggregator's background-refresher wiring. It records the family IDs passed to
// RefreshSessionProvider so a test can assert the background SSO re-exchange
// path uses the provider-only (non-client-rotating) refresh.
type fakeOAuthServer struct {
	refreshedFamilies []string
	refreshErr        error
}

func (f *fakeOAuthServer) SetOnAuthenticated(func(context.Context, string))        {}
func (f *fakeOAuthServer) ValidateTokenWithSubject(next http.Handler) http.Handler { return next }
func (f *fakeOAuthServer) CreateMux() http.Handler                                 { return http.NewServeMux() }
func (f *fakeOAuthServer) Shutdown(context.Context) error                          { return nil }
func (f *fakeOAuthServer) RefreshSessionProvider(_ context.Context, familyID string) error {
	f.refreshedFamilies = append(f.refreshedFamilies, familyID)
	return f.refreshErr
}

// TestSessionRefresher_UsesProviderOnlyRefresh guards the wiring so a refactor
// cannot silently re-point the background refresher at a client-token-rotating
// path; see oauthServer.RefreshSessionProvider for the rotation/deauth
// background (giantswarm#37164). The no-rotation invariant itself is proven in
// mcp-oauth (TestRefreshSessionProvider_DoesNotRotateClientRefreshToken).
func TestSessionRefresher_UsesProviderOnlyRefresh(t *testing.T) {
	fake := &fakeOAuthServer{}
	a := &AggregatorServer{oauthHTTPServer: fake}

	refresher := a.sessionRefresher()
	if refresher == nil {
		t.Fatal("sessionRefresher must be non-nil when the OAuth server is configured")
	}

	if err := refresher(context.Background(), "fam-123"); err != nil {
		t.Fatalf("refresher returned error: %v", err)
	}

	if len(fake.refreshedFamilies) != 1 || fake.refreshedFamilies[0] != "fam-123" {
		t.Fatalf("sessionRefresher must delegate to the provider-only RefreshSessionProvider; recorded families = %v", fake.refreshedFamilies)
	}
}

// TestSessionRefresher_NilWhenOAuthDisabled documents that the refresher is nil
// when no OAuth server is configured, so getIDTokenForForwarding skips the
// in-process refresh entirely rather than dereferencing a nil server.
func TestSessionRefresher_NilWhenOAuthDisabled(t *testing.T) {
	if refresher := (&AggregatorServer{}).sessionRefresher(); refresher != nil {
		t.Fatal("sessionRefresher must be nil when no OAuth server is configured")
	}
}
