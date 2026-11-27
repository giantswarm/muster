package metatools

import (
	"context"
	"strings"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// signInForSearch answers an agent's filter_tools search that names exactly
// one server awaiting the person's sign-in with that server's sign-in, as a
// call of one of its tools is answered: muster's auth_required answer, which
// the URL elicitation middleware turns into the sign-in prompt the person's
// client shows (api.SignInPrompt). Without it the agent only reads the server
// in servers_requiring_auth and tells the person to sign in, the link
// reaching them only when they ask for it.
//
// It answers nothing (nil, false) when the request is not marked
// api.SignInPrompt, when the search names no such server or several, when the
// person cannot sign in from this session (no auth_tool), or when the sign-in
// could not be started; the listing is then answered as before. connected
// says a grant the person already holds connected the session instead: the
// listing is then made again and shows the server's tools.
func signInForSearch(ctx context.Context, handler api.MetaToolsHandler, pending []api.ServerAuthInfo, opts filterToolsOptions) (result *api.CallToolResult, connected bool) {
	if !opts.offerSignIn || !api.SignInPrompt(ctx) {
		return nil, false
	}
	server, ok := namedSignInServer(pending, opts)
	if !ok {
		return nil, false
	}
	answer, connected, err := handler.AnswerSignIn(ctx, server.Name)
	if err != nil {
		logging.Warn("metatools", "filter_tools: the sign-in to %s could not be started, answering the listing: %v", server.Name, err)
		return nil, false
	}
	if connected || answer == nil {
		return nil, connected
	}
	return fromMCPResult(answer), false
}

// namedSignInServer returns the one server awaiting sign-in the search names:
// its pattern's literal head lies under the server's tool prefix (x_miro_*,
// or a tool name of it), or its query or description filter has the server's
// name as a word. A search naming none, or several, names no server: a broad
// search must not ask the person to sign in to anything.
func namedSignInServer(pending []api.ServerAuthInfo, opts filterToolsOptions) (api.ServerAuthInfo, bool) {
	var named []api.ServerAuthInfo
	for _, server := range pending {
		if server.AuthTool == "" {
			continue
		}
		if patternNamesPrefix(opts.pattern, server.ToolPrefix, opts.caseSensitive) ||
			containsWord(opts.query, server.Name) || containsWord(opts.descriptionFilter, server.Name) {
			named = append(named, server)
		}
	}
	if len(named) != 1 {
		return api.ServerAuthInfo{}, false
	}
	return named[0], true
}

// patternNamesPrefix reports whether every name the glob pattern matches lies
// under prefix: its literal head, up to the first metacharacter, starts with
// the prefix. A pattern that only could match there (x_*) names nothing.
func patternNamesPrefix(pattern, prefix string, caseSensitive bool) bool {
	if pattern == "" || prefix == "" {
		return false
	}
	if !caseSensitive {
		pattern, prefix = strings.ToLower(pattern), strings.ToLower(prefix)
	}
	head := pattern
	if i := strings.IndexAny(pattern, `*?[\`); i >= 0 {
		head = pattern[:i]
	}
	return strings.HasPrefix(head, prefix)
}

// containsWord reports whether text has word in it, case-insensitively, not
// as a part of a longer word: "miro" in "my miro boards", not in "miroslav".
func containsWord(text, word string) bool {
	if text == "" || word == "" {
		return false
	}
	text, word = strings.ToLower(text), strings.ToLower(word)
	for start := 0; ; {
		i := strings.Index(text[start:], word)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(word)
		if (i == 0 || !isWordRune(lastRune(text[:i]))) && (end == len(text) || !isWordRune(firstRune(text[end:]))) {
			return true
		}
		start = i + 1
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	r := rune(0)
	for _, c := range s {
		r = c
	}
	return r
}

// fromMCPResult carries an MCP tool result as a meta-tool result: content
// items pass through natively, the structured content and the error mark are
// kept, so the URL elicitation middleware reads the sign-in link from it.
func fromMCPResult(result *mcp.CallToolResult) *api.CallToolResult {
	content := make([]any, 0, len(result.Content))
	for _, c := range result.Content {
		if text, ok := mcp.AsTextContent(c); ok {
			content = append(content, text.Text)
			continue
		}
		content = append(content, c)
	}
	return &api.CallToolResult{Content: content, IsError: result.IsError, StructuredContent: result.StructuredContent}
}
