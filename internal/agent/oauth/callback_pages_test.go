package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/giantswarm/muster/v5/internal/signinpage"
)

// requestPage sends one request to the callback handler without a listener,
// in its own goroutine, since the handler waits for the sign-in's outcome.
// The returned channel delivers the response once the handler has written it.
func requestPage(s *CallbackServer, query string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		s.handleCallback(rr, httptest.NewRequest(http.MethodGet, "/callback?"+query, nil))
		done <- rr
	}()
	return done
}

func assertPage(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, want ...string) {
	t.Helper()
	if rr.Code != wantStatus {
		t.Errorf("expected status %d, got %d", wantStatus, rr.Code)
	}
	for _, w := range want {
		if !strings.Contains(rr.Body.String(), w) {
			t.Errorf("expected body to contain %q, got %q", w, rr.Body.String())
		}
	}
}

// TestCallbackServer_PageWaitsForOutcome covers the page showing the sign-in's
// outcome, not the arrival of the callback: nothing is written before Finish.
func TestCallbackServer_PageWaitsForOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewCallbackServer(0)
		page := requestPage(s, "code=c&state=s")

		synctest.Wait()
		select {
		case <-page:
			t.Fatal("expected no response before the outcome is known")
		default:
		}
		if result := <-s.resultCh; result.Code != "c" || result.State != "s" {
			t.Errorf("expected the callback's parameters on the result, got %+v", result)
		}

		s.Finish(http.StatusBadRequest, signinpage.Failed("The sign-in response could not be verified", ""))

		assertPage(t, <-page, http.StatusBadRequest, "<h1>Sign-in failed</h1>", "could not be verified")
	})
}

func TestCallbackServer_PageWithoutOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewCallbackServer(0)
		page := requestPage(s, "code=c&state=s")

		// No Finish: the page stops waiting before the server's write timeout.
		assertPage(t, <-page, http.StatusAccepted, "Muster is still completing the sign-in")
	})
}

func TestCallbackServer_StopFinishesWaitingPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewCallbackServer(0)
		page := requestPage(s, "code=c&state=s")
		synctest.Wait()

		s.Stop()

		assertPage(t, <-page, http.StatusBadRequest, "<h1>Sign-in failed</h1>", "The sign-in was cancelled")
	})
}

// TestCallbackServer_DuplicateDeliveryShowsFirstOutcome covers a browser or
// provider redirect page that loads the callback twice: only the first load
// reaches the client, and both show its outcome.
func TestCallbackServer_DuplicateDeliveryShowsFirstOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewCallbackServer(0)
		first := requestPage(s, "code=c&state=s")
		second := requestPage(s, "code=other&state=other")
		synctest.Wait()

		if result := <-s.resultCh; result.Code != "c" && result.Code != "other" {
			t.Errorf("unexpected result %+v", result)
		}
		select {
		case result := <-s.resultCh:
			t.Errorf("expected one result, got a second one: %+v", result)
		default:
		}

		s.Finish(http.StatusOK, signinpage.SignedIn("Muster"))

		assertPage(t, <-first, http.StatusOK, "<h1>Signed in</h1>")
		assertPage(t, <-second, http.StatusOK, "<h1>Signed in</h1>")
	})
}

func TestCallbackServer_SecurityHeaders(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackURL, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	// The page waits for the outcome; finish it so the request answers at once.
	server.Finish(http.StatusOK, signinpage.SignedIn("Muster"))

	// Make a request and check the response headers
	resp, err := http.Get(callbackURL + "?code=test-code&state=test-state")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check security headers
	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	}

	for header, expectedValue := range expectedHeaders {
		actual := resp.Header.Get(header)
		if actual != expectedValue {
			t.Errorf("expected header %s=%q, got %q", header, expectedValue, actual)
		}
	}

	// Check CSP header (may have varying format)
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Error("expected Content-Security-Policy header")
	} else if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy should forbid loading and framing, got: %s", csp)
	}
}
