package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/agent"
)

const elicitationURL = "https://dex.example.com/auth?state=abc"

var signIn = agent.URLElicitation{Key: "muster_sign_in", Message: "Sign in to github to continue.", URL: elicitationURL}

func TestCheckElicitationURL(t *testing.T) {
	tests := []struct {
		url     string
		host    string
		refusal string
	}{
		{url: elicitationURL, host: "dex.example.com"},
		{url: "https://dex.example.com:8443/auth", host: "dex.example.com:8443"},
		{url: "http://localhost:8090/oauth/proxy/start", host: "localhost:8090"},
		{url: "http://127.0.0.1:8090/start", host: "127.0.0.1:8090"},
		{url: "http://[::1]:8090/start", host: "[::1]:8090"},
		{url: "http://dex.example.com/auth", refusal: "not https"},
		{url: "javascript:alert(1)", refusal: "not an absolute URL"},
		{url: "file:///etc/passwd", refusal: "not an absolute URL"},
		{url: "ftp://dex.example.com/x", refusal: "not https"},
		{url: "/relative", refusal: "not an absolute URL"},
		{url: "https://user:pass@dex.example.com/auth", refusal: "credentials"},
		{url: "https://good.example.com@evil.example.com/", refusal: "credentials"},
		{url: "https://dex.example.com/a b", refusal: "invisible"},
		{url: "https://dex.example.com/\u202eevil", refusal: "invisible"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			host, err := checkElicitationURL(tt.url)
			if tt.refusal != "" {
				require.ErrorContains(t, err, tt.refusal)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.host, host)
		})
	}
}

// testElicitor is an interactive elicitor reading input, whose Ctrl-C is
// interrupt.
func testElicitor(input io.Reader, interrupt context.Context) (*urlElicitor, *bytes.Buffer, *[]string) {
	var out bytes.Buffer
	var opened []string
	u := &urlElicitor{
		out:         &out,
		in:          input,
		interactive: true,
		open: func(url string) error {
			opened = append(opened, url)
			return nil
		},
		interrupted: func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(interrupt, cancel)
			return ctx, func() { stop(); cancel() }
		},
	}
	return u, &out, &opened
}

func TestURLElicitorShowsMessageHostAndURL(t *testing.T) {
	u, out, opened := testElicitor(strings.NewReader("\n\n"), t.Context())

	actions, err := u.answer(t.Context(), []agent.URLElicitation{signIn})
	require.NoError(t, err)
	require.Equal(t, []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionAccept}, actions)
	require.Equal(t, []string{elicitationURL}, *opened)
	require.Equal(t, `
This call needs you to open a page:
  Sign in to github to continue.
  Destination: dex.example.com
  URL:         https://dex.example.com/auth?state=abc
Open it in your browser? [Y/n] Opened in your browser.
When you are done on the page, press Enter to continue, or type d to decline or c to cancel: `, out.String())
}

func TestURLElicitorAnswers(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		action mcp.ElicitationResponseAction
		opened bool
	}{
		{name: "open then done", input: "y\ndone\n", action: mcp.ElicitationResponseActionAccept, opened: true},
		{name: "open it yourself then done", input: "n\n\n", action: mcp.ElicitationResponseActionAccept},
		{name: "decline", input: "n\nd\n", action: mcp.ElicitationResponseActionDecline},
		{name: "cancel", input: "y\ncancel\n", action: mcp.ElicitationResponseActionCancel, opened: true},
		{name: "unknown answers are asked again", input: "maybe\nn\nlater\ndecline\n", action: mcp.ElicitationResponseActionDecline},
		{name: "end of input cancels", input: "n\n", action: mcp.ElicitationResponseActionCancel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _, opened := testElicitor(strings.NewReader(tt.input), t.Context())

			actions, err := u.answer(t.Context(), []agent.URLElicitation{signIn})
			require.NoError(t, err)
			require.Equal(t, []mcp.ElicitationResponseAction{tt.action}, actions)
			require.Equal(t, tt.opened, len(*opened) == 1)
		})
	}
}

func TestURLElicitorAutoOpenDoesNotAsk(t *testing.T) {
	u, out, opened := testElicitor(strings.NewReader("\n"), t.Context())
	u.autoOpen = true
	u.open = func(url string) error {
		*opened = append(*opened, url)
		return errors.New("no display")
	}

	actions, err := u.answer(t.Context(), []agent.URLElicitation{signIn})
	require.NoError(t, err)
	require.Equal(t, []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionAccept}, actions)
	require.Equal(t, []string{elicitationURL}, *opened)
	require.NotContains(t, out.String(), "Open it in your browser?")
	require.Contains(t, out.String(), "Could not open a browser (no display): open the URL yourself.")
}

func TestURLElicitorDeclinesARefusedURLWithoutAsking(t *testing.T) {
	u, out, opened := testElicitor(strings.NewReader(""), t.Context())
	refused := agent.URLElicitation{Key: "github", Message: "[github]\nApprove", URL: "http://evil.example.com/x"}

	actions, err := u.answer(t.Context(), []agent.URLElicitation{refused})
	require.NoError(t, err)
	require.Equal(t, []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionDecline}, actions)
	require.Empty(t, *opened)
	require.Contains(t, out.String(), "  [github] Approve\n")
	require.Contains(t, out.String(), `URL refused: it is not https. muster does not open it: "http://evil.example.com/x"`)
	require.NotContains(t, out.String(), "Destination:")
}

func TestURLElicitorInterruptCancelsEveryElicitation(t *testing.T) {
	interrupt, press := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	u, out, _ := testElicitor(reader, interrupt)
	u.autoOpen = true
	press()

	actions, err := u.answer(t.Context(), []agent.URLElicitation{signIn, {Key: "b", Message: "b", URL: elicitationURL}})
	require.NoError(t, err)
	require.Equal(t, []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionCancel, mcp.ElicitationResponseActionCancel}, actions)
	require.Contains(t, out.String(), "Cancelled.")
}

func TestURLElicitorEndedContextIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	u, _, _ := testElicitor(reader, t.Context())
	cancel()

	_, err := u.answer(ctx, []agent.URLElicitation{signIn})
	require.ErrorIs(t, err, context.Canceled)
}

func TestURLElicitorPausesTheSpinner(t *testing.T) {
	u, _, _ := testElicitor(strings.NewReader("\n\n"), t.Context())
	var calls []string
	u.setSpinner(func() { calls = append(calls, "pause") }, func() { calls = append(calls, "resume") })

	_, err := u.answer(t.Context(), []agent.URLElicitation{signIn})
	require.NoError(t, err)
	require.Equal(t, []string{"pause", "resume"}, calls)
}

func TestURLElicitorNonInteractiveFailsWithTheURL(t *testing.T) {
	u, out, opened := testElicitor(strings.NewReader("\n\n"), t.Context())
	u.interactive = false

	_, err := u.answer(t.Context(), []agent.URLElicitation{signIn})
	var inputRequired *InputRequiredError
	require.ErrorAs(t, err, &inputRequired)
	require.Equal(t, `input_required: this call needs a page opened in a browser, and there is no terminal to ask on

  Sign in to github to continue.
  Destination: dex.example.com
  URL:         https://dex.example.com/auth?state=abc

Open the URL in a browser, finish there, then run the command again.`, err.Error())
	require.Empty(t, out.String())
	require.Empty(t, *opened)
}
