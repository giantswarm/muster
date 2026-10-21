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

// TestFailed_ShowsOnlyRegisteredErrorCodes checks that Failed filters the
// code through pagekit: a registered code that muster's earlier list lacked
// is shown, and text that isn't a registered code is not.
func TestFailed_ShowsOnlyRegisteredErrorCodes(t *testing.T) {
	tests := []struct {
		code       string
		wantDetail string
	}{
		{code: "invalid_client", wantDetail: "invalid_client"},
		{code: "your_account_is_locked_email_help_at_evil_example", wantDetail: ""},
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

func TestPending(t *testing.T) {
	p := Pending("Muster")

	if p.Heading != "Sign-in received" || p.Summary != "Muster is still completing the sign-in" {
		t.Errorf("unexpected page %+v", p)
	}
	if p.Message != "Check where you started the sign-in to see whether it completed. You can close this tab." {
		t.Errorf("unexpected message %q", p.Message)
	}
}
