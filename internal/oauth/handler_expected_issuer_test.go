package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// TestHandler_HandleCallback_PinnedIdentityExpectedIssuer covers RFC 9207 on a
// fully pinned authorization server (explicit endpoints, no discovery) whose
// pinned identity is not the issuer identifier it puts in `iss` -- a GitHub
// App pinned under its own identity while GitHub sends its login issuer. With
// IssuerPin.ExpectedIssuer the response is compared against that identifier;
// without it the pinned identity is expected, as before. The identity stays
// the key the flow is filed under either way.
//
// A response that passes the check goes on to the token request against the
// pinned token endpoint, which does not exist here, so the two outcomes are
// told apart by the message on the error page.
func TestHandler_HandleCallback_PinnedIdentityExpectedIssuer(t *testing.T) {
	const (
		identity   = "https://as.example.com/apps/one"
		realIssuer = "https://as.example.com/login/oauth"
		authEP     = "https://as.example.com/login/oauth/authorize"
		tokenEP    = "https://as.example.com/login/oauth/access_token"

		issAccepted = "Failed to complete authentication"
		issRejected = "could not be verified"
	)

	tests := []struct {
		name           string
		expectedIssuer string
		iss            string
		wantInBody     string
	}{
		{
			name:       "without expectedIssuer the pinned identity is expected",
			iss:        identity,
			wantInBody: issAccepted,
		},
		{
			name:       "without expectedIssuer the server's real issuer is refused",
			iss:        realIssuer,
			wantInBody: issRejected,
		},
		{
			name:           "expectedIssuer accepts the server's real issuer",
			expectedIssuer: realIssuer,
			iss:            realIssuer,
			wantInBody:     issAccepted,
		},
		{
			name:           "expectedIssuer with a trailing slash",
			expectedIssuer: realIssuer + "/",
			iss:            realIssuer,
			wantInBody:     issAccepted,
		},
		{
			name:           "response with a trailing slash",
			expectedIssuer: realIssuer,
			iss:            realIssuer + "/",
			wantInBody:     issAccepted,
		},
		{
			name:           "expectedIssuer refuses the pinned identity",
			expectedIssuer: realIssuer,
			iss:            identity,
			wantInBody:     issRejected,
		},
		{
			name:           "expectedIssuer refuses another authorization server",
			expectedIssuer: realIssuer,
			iss:            "https://evil.example.com",
			wantInBody:     issRejected,
		},
		{
			name:           "absent iss stays accepted with expectedIssuer",
			expectedIssuer: realIssuer,
			iss:            "",
			wantInBody:     issAccepted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
			defer client.Stop()

			client.PinIssuer(identity, IssuerPin{ClientID: "Iv23liOne", ClientSecret: "s3cr3t", ExpectedIssuer: tc.expectedIssuer},
				&pkgoauth.Metadata{Issuer: identity, AuthorizationEndpoint: authEP, TokenEndpoint: tokenEP})

			handler := NewHandler(client)
			encodedState := storeCallbackState(t, client, identity)

			query := url.Values{
				"code":  {"auth-code"},
				"state": {encodedState},
			}
			if tc.iss != "" {
				query.Set("iss", tc.iss)
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/callback?"+query.Encode(), nil)
			recorder := httptest.NewRecorder()
			handler.HandleCallback(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
			}
			if body := recorder.Body.String(); !strings.Contains(body, tc.wantInBody) {
				t.Errorf("expected the error page to say %q, got %q", tc.wantInBody, body)
			}
		})
	}
}

// TestClient_ExpectedResponseIssuer covers the lookup the callback uses: the
// pin's ExpectedIssuer, tolerant of a trailing slash on the pinned identity,
// and empty for an issuer without a pin or a pin without the field.
func TestClient_ExpectedResponseIssuer(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	client.PinIssuer("https://as.example.com/apps/one/", IssuerPin{ExpectedIssuer: "https://as.example.com"}, pinnedMetadata())
	client.PinIssuer("https://as.example.com/apps/two", IssuerPin{SubjectScoped: true}, pinnedMetadata())

	if got := client.expectedResponseIssuer("https://as.example.com/apps/one"); got != "https://as.example.com" {
		t.Errorf("expectedResponseIssuer(one) = %q, want the pin's expected issuer", got)
	}
	if got := client.expectedResponseIssuer("https://as.example.com/apps/two"); got != "" {
		t.Errorf("expectedResponseIssuer(two) = %q, want empty for a pin without the field", got)
	}
	if got := client.expectedResponseIssuer("https://unpinned.example.com"); got != "" {
		t.Errorf("expectedResponseIssuer(unpinned) = %q, want empty", got)
	}
}
