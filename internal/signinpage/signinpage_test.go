package signinpage

import "testing"

func TestSignedIn(t *testing.T) {
	p := SignedIn("Marge")

	if p.Title != "Signed in to Marge" || p.Heading != "Signed in" || p.Summary != "Marge is now connected" {
		t.Errorf("unexpected page %+v", p)
	}
	if p.Message != "You can close this tab and return to where you started the sign-in." {
		t.Errorf("unexpected message %q", p.Message)
	}
}

func TestFailed_ShowsOnlyKnownErrorCodes(t *testing.T) {
	tests := []struct {
		code       string
		wantDetail string
	}{
		{code: "access_denied", wantDetail: "access_denied"},
		{code: "login_required", wantDetail: "login_required"},
		{code: "invalid_target", wantDetail: "invalid_target"},
		{code: "", wantDetail: ""},
		{code: "your_account_is_locked_email_help_at_evil_example", wantDetail: ""},
		{code: "Call support at 555", wantDetail: ""},
		{code: "ACCESS_DENIED", wantDetail: ""},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			p := Failed("The sign-in was denied or failed", tc.code)

			if p.Detail != tc.wantDetail {
				t.Errorf("expected detail %q, got %q", tc.wantDetail, p.Detail)
			}
			if p.Heading != "Sign-in failed" || p.Summary != "The sign-in was denied or failed" {
				t.Errorf("unexpected page %+v", p)
			}
			if p.Message != "You can close this tab and start the sign-in again from where you started it." {
				t.Errorf("unexpected message %q", p.Message)
			}
		})
	}
}
