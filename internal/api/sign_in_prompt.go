package api

import "context"

// signInPromptKey marks a request whose client shows the person a sign-in
// link itself (MCP URL mode elicitation) and that an agent makes for the
// person.
type signInPromptKey struct{}

// WithSignInPrompt returns a context marking the request as one whose client
// shows a sign-in link to the person directly: the client declared URL mode
// elicitation and the caller is an agent acting for the person. muster then
// answers an agent's search for the tools of a server awaiting the person's
// sign-in with that sign-in, as it answers a call of such a tool, instead of
// leaving the agent to tell the person to sign in somewhere.
func WithSignInPrompt(ctx context.Context) context.Context {
	return context.WithValue(ctx, signInPromptKey{}, true)
}

// SignInPrompt reports whether the request was marked by WithSignInPrompt.
func SignInPrompt(ctx context.Context) bool {
	marked, _ := ctx.Value(signInPromptKey{}).(bool)
	return marked
}
