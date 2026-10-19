package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// servePage sends one request to the callback handler without a listener.
// Callers run it in synctest.Test and end with settle.
func servePage(s *CallbackServer, query string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.handleCallback(rr, httptest.NewRequest(http.MethodGet, "/callback?"+query, nil))
	return rr
}

// settle lets the handler's delayed shutdown finish. The bubble's clock is
// fake, so this returns at once; a bubble may not end with the shutdown
// goroutine still asleep.
func settle() {
	time.Sleep(time.Minute)
}

func TestCallbackServer_Pages(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		want       []string
		notWant    []string
	}{
		{
			name:       "success",
			query:      "code=c&state=s",
			wantStatus: http.StatusOK,
			want:       []string{"<h1>Signed in</h1>", "Muster is now connected"},
		},
		{
			name:       "no code",
			query:      "state=s",
			wantStatus: http.StatusBadRequest,
			want:       []string{"<h1>Sign-in failed</h1>", "The sign-in response is incomplete"},
			notWant:    []string{"Signed in"},
		},
		{
			name:       "error shows the code, not the description",
			query:      "error=access_denied&error_description=Call+support+at+555",
			wantStatus: http.StatusBadRequest,
			want:       []string{"<h1>Sign-in failed</h1>", "<code>access_denied</code>"},
			notWant:    []string{"555"},
		},
		{
			name:       "error with an unknown code",
			query:      "error=your_account_is_locked_call_555",
			wantStatus: http.StatusBadRequest,
			want:       []string{"<h1>Sign-in failed</h1>"},
			notWant:    []string{"555"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rr := servePage(NewCallbackServer(0), tc.query)

				if rr.Code != tc.wantStatus {
					t.Errorf("expected status %d, got %d", tc.wantStatus, rr.Code)
				}
				body := rr.Body.String()
				for _, want := range tc.want {
					if !strings.Contains(body, want) {
						t.Errorf("expected body to contain %q, got %q", want, body)
					}
				}
				for _, notWant := range tc.notWant {
					if strings.Contains(body, notWant) {
						t.Errorf("expected body not to contain %q", notWant)
					}
				}
				settle()
			})
		})
	}
}

// TestCallbackServer_DuplicateDeliveryShowsFirstOutcome covers a browser or
// provider redirect page that loads the callback twice: the second load shows
// what the first one did, not a page of its own.
func TestCallbackServer_DuplicateDeliveryShowsFirstOutcome(t *testing.T) {
	tests := []struct {
		name       string
		first      string
		wantStatus int
		wantH1     string
	}{
		{name: "after success", first: "code=c&state=s", wantStatus: http.StatusOK, wantH1: "<h1>Signed in</h1>"},
		{name: "after an error", first: "error=access_denied", wantStatus: http.StatusBadRequest, wantH1: "<h1>Sign-in failed</h1>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := NewCallbackServer(0)
				servePage(s, tc.first)

				rr := servePage(s, "code=other&state=other")

				if rr.Code != tc.wantStatus {
					t.Errorf("expected status %d, got %d", tc.wantStatus, rr.Code)
				}
				if !strings.Contains(rr.Body.String(), tc.wantH1) {
					t.Errorf("expected body to contain %q, got %q", tc.wantH1, rr.Body.String())
				}
				settle()
			})
		})
	}
}
