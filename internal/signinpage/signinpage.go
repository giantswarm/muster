package signinpage

import "github.com/giantswarm/pagekit"

// knownErrorCodes are the error codes an authorization response may carry:
// RFC 6749 section 4.1.2.1, OpenID Connect Core section 3.1.2.6 and RFC 8707.
// A page shows the code only when it is one of these, since anyone can send
// a browser to a callback with an error parameter of their choosing.
var knownErrorCodes = map[string]bool{
	"invalid_request":            true,
	"unauthorized_client":        true,
	"access_denied":              true,
	"unsupported_response_type":  true,
	"invalid_scope":              true,
	"server_error":               true,
	"temporarily_unavailable":    true,
	"interaction_required":       true,
	"login_required":             true,
	"account_selection_required": true,
	"consent_required":           true,
	"invalid_request_uri":        true,
	"invalid_request_object":     true,
	"request_not_supported":      true,
	"request_uri_not_supported":  true,
	"registration_not_supported": true,
	"invalid_target":             true,
}

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
	p := pagekit.Page{
		Heading: "Sign-in failed",
		Summary: summary,
		Message: "You can close this tab and start the sign-in again from where you started it.",
	}
	if knownErrorCodes[errorCode] {
		p.Detail = errorCode
	}
	return p
}
