// Package chatgptlogin is OpenAI's device-code sign-in to ChatGPT, the one
// the Codex CLI runs with `codex login --device-auth`: muster asks for a user
// code, the person enters it at OpenAI's device page, muster polls until the
// sign-in is authorized and exchanges the authorization code it is handed,
// with its PKCE verifier, for the person's tokens.
//
// It is OpenAI's own flow, not RFC 8628: the user code comes from
// /api/accounts/deviceauth/usercode, the poll answers 403 or 404 while the
// code is outstanding and, once it is entered, an authorization code with its
// PKCE verifier, redeemed at /oauth/token with the redirect URI
// /deviceauth/callback. A code is given up after 15 minutes, as codex does.
//
// The package holds no state: the caller keeps the outstanding code and the
// resulting grant.
package chatgptlogin
