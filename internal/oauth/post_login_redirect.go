package oauth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/giantswarm/muster/pkg/logging"
)

// redirectAllowed reports whether a caller-supplied post-login redirect
// target matches the operator allowlist: absolute http(s), no userinfo,
// scheme/host equal to an entry, and the entry's path extended at a segment
// boundary. Matching uses the escaped path form so percent-encoded slashes
// cannot fake a boundary, and dot segments are rejected outright: browsers
// resolve them before navigation, which would void the path constraint. The
// target's query is unconstrained so front-ends can carry their own
// correlation state.
func (h *Handler) redirectAllowed(raw string) bool {
	target, err := url.Parse(raw)
	if err != nil || target.User != nil || target.Host == "" ||
		(target.Scheme != "https" && target.Scheme != "http") {
		return false
	}
	targetPath := target.EscapedPath()
	if hasDotSegment(targetPath) {
		return false
	}
	for _, entry := range h.postLoginRedirectAllowlist {
		if target.Scheme == entry.Scheme && target.Host == entry.Host &&
			pathExtendsPrefix(targetPath, entry.EscapedPath()) {
			return true
		}
	}
	return false
}

// hasDotSegment reports whether an escaped URL path contains a "." or ".."
// segment, in plain or %2e-encoded form (browsers treat "%2e%2e" and mixed
// spellings as dot segments during navigation).
func hasDotSegment(escapedPath string) bool {
	for segment := range strings.SplitSeq(escapedPath, "/") {
		decoded := strings.ReplaceAll(strings.ToLower(segment), "%2e", ".")
		if decoded == "." || decoded == ".." {
			return true
		}
	}
	return false
}

// pathExtendsPrefix reports whether an escaped target path equals an escaped
// entry path or extends it at a segment boundary: entry "/connectors"
// matches "/connectors" and "/connectors/complete" but not "/connectorsevil".
// An entry path of "" or "/" admits every path on the entry's host.
func pathExtendsPrefix(targetPath, entryPath string) bool {
	entryPath = strings.TrimSuffix(entryPath, "/")
	if entryPath == "" {
		return true
	}
	return targetPath == entryPath || strings.HasPrefix(targetPath, entryPath+"/")
}

// finishSuccess completes a successful callback: a redirect to the flow's
// recorded post-login target when the start endpoint accepted one, the
// static success page otherwise. The target was allowlist-validated at the
// start endpoint; the callback never reads it from request input.
func (h *Handler) finishSuccess(w http.ResponseWriter, r *http.Request, state *OAuthState) {
	if state.RedirectURI != "" {
		target, err := url.Parse(state.RedirectURI)
		if err == nil {
			http.Redirect(w, r, postLoginRedirectTarget(target, state.ServerName), http.StatusSeeOther) //nolint:gosec // G710: allowlist-validated at the start endpoint, stored server-side
			return
		}
		logging.Warn("OAuth", "Ignoring unparseable post-login redirect target: %v", err)
	}
	h.renderSuccessPage(w, state.ServerName)
}

// postLoginRedirectTarget appends the connected server's name to the
// post-login redirect URL, preserving any query parameters already on it
// (front-ends carry their own correlation state there).
func postLoginRedirectTarget(base *url.URL, serverName string) string {
	target := *base
	query := target.Query()
	query.Set("server", serverName)
	target.RawQuery = query.Encode()
	return target.String()
}
