package signinpage

import "github.com/giantswarm/pagekit"

// SignedIn returns the page for a completed sign-in to name.
func SignedIn(name string) pagekit.Page {
	return pagekit.Page{
		Title:   "Signed in to " + name,
		Heading: "Signed in",
		Summary: name + " is now connected",
		Message: "You can close this tab and return to where you started the sign-in.",
	}
}

// Pending returns the page for a sign-in whose outcome name has not reported
// in time.
func Pending(name string) pagekit.Page {
	return pagekit.Page{
		Heading: "Sign-in received",
		Summary: name + " is still completing the sign-in",
		Message: "Check where you started the sign-in to see whether it completed. You can close this tab.",
	}
}

// Failed returns the page for a failed sign-in. summary names the cause.
// errorCode is the error parameter of the authorization response, if any; it
// is shown only when it is a known error code.
func Failed(summary, errorCode string) pagekit.Page {
	return pagekit.Page{
		Heading: "Sign-in failed",
		Summary: summary,
		Message: "You can close this tab and start the sign-in again from where you started it.",
		Detail:  pagekit.OAuthErrorCode(errorCode),
	}
}
