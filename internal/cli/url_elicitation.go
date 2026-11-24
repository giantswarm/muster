package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/agent"
	"github.com/giantswarm/muster/v5/internal/agent/oauth"
)

// InputRequiredMarker opens every InputRequiredError message so a script or
// an agent can tell "open this page first" from any other failure.
const InputRequiredMarker = "input_required"

// InputRequiredError is the answer to a tool call that needs the person to
// open a page when the CLI has no terminal to ask on. It lists each page, and
// the process exits with the auth-required exit code: the person opens the
// page, then runs the command again.
type InputRequiredError struct {
	Elicitations []agent.URLElicitation
}

// Error lists each elicitation as the terminal prompt shows it.
func (e *InputRequiredError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: this call needs a page opened in a browser, and there is no terminal to ask on\n", InputRequiredMarker)
	for _, elicitation := range e.Elicitations {
		b.WriteString("\n")
		b.WriteString(describeElicitation(elicitation))
	}
	b.WriteString("\nOpen the URL in a browser, finish there, then run the command again.")
	return b.String()
}

// Is allows errors.Is() to work with wrapped errors.
func (e *InputRequiredError) Is(target error) bool {
	_, ok := target.(*InputRequiredError)
	return ok
}

// describeElicitation shows the server's message, the page's host and its
// URL, or why the URL is refused, one per line.
func describeElicitation(elicitation agent.URLElicitation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n", oneLine(elicitation.Message))
	host, err := checkElicitationURL(elicitation.URL)
	if err != nil {
		fmt.Fprintf(&b, "  URL refused: %v. muster does not open it: %q\n", err, elicitation.URL)
		return b.String()
	}
	fmt.Fprintf(&b, "  Destination: %s\n", host)
	fmt.Fprintf(&b, "  URL:         %s\n", elicitation.URL)
	return b.String()
}

// oneLine shows a server's message on one line, its control and format
// characters removed, so that it cannot draw lines of its own.
func oneLine(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, message)
	if message = strings.Join(strings.Fields(message), " "); message == "" {
		return "The server asks you to open a page to continue."
	}
	return message
}

// checkElicitationURL returns the host of a URL the person may be asked to
// open: absolute https, or http on a loopback host (a muster running
// locally), without credentials, and shown exactly as written (no spaces,
// control or format characters). Otherwise it says why the URL is refused.
func checkElicitationURL(raw string) (string, error) {
	if strings.ContainsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) {
		return "", errors.New("it holds spaces or invisible characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("it is not an absolute URL")
	}
	if u.User != nil {
		return "", errors.New("it carries credentials (user info)")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", errors.New("it is not https")
		}
	default:
		return "", errors.New("it is not https")
	}
	return u.Host, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// urlElicitor asks the person at the terminal about the URL elicitations of a
// tool call: it shows each server's message, the page's host and URL, offers
// to open it in the browser, and asks the person to confirm when they are
// done (accept), or to decline or cancel. A URL it refuses is declined
// without asking. Ctrl-C cancels. Without a terminal it asks nothing and
// fails the call with InputRequiredError.
type urlElicitor struct {
	out         io.Writer
	interactive bool
	// autoOpen opens each page without asking first (--login).
	autoOpen bool
	open     func(url string) error
	// interrupted returns a context ended by Ctrl-C, and its stop function.
	interrupted func(ctx context.Context) (context.Context, context.CancelFunc)

	in        io.Reader
	linesOnce sync.Once
	lines     chan string

	mu sync.Mutex
	// pause and resume stop the call's spinner while the person is asked.
	pause, resume func()
}

// newURLElicitor returns the elicitor of a CLI command: it asks on stdin and
// writes to stderr, when both are terminals and the command allows it.
func newURLElicitor(interactive, autoOpen bool) *urlElicitor {
	return &urlElicitor{
		out:         os.Stderr,
		in:          os.Stdin,
		interactive: interactive,
		autoOpen:    autoOpen,
		open:        oauth.OpenBrowser,
		interrupted: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt)
		},
	}
}

// setSpinner registers the functions that stop and restart the call's
// progress display around a prompt; nil functions register none.
func (u *urlElicitor) setSpinner(pause, resume func()) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pause, u.resume = pause, resume
}

// answer is the agent.URLElicitationHandler of the command's client.
func (u *urlElicitor) answer(ctx context.Context, elicitations []agent.URLElicitation) ([]mcp.ElicitationResponseAction, error) {
	if !u.interactive {
		return nil, &InputRequiredError{Elicitations: elicitations}
	}
	u.mu.Lock()
	pause, resume := u.pause, u.resume
	u.mu.Unlock()
	if pause != nil {
		pause()
	}
	if resume != nil {
		defer resume()
	}

	askCtx, stop := u.interrupted(ctx)
	defer stop()

	actions := make([]mcp.ElicitationResponseAction, 0, len(elicitations))
	cancelled := false
	for _, elicitation := range elicitations {
		if cancelled {
			actions = append(actions, mcp.ElicitationResponseActionCancel)
			continue
		}
		action, err := u.ask(askCtx, elicitation)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			u.say("\nCancelled.\n")
			action = mcp.ElicitationResponseActionCancel
		}
		cancelled = action == mcp.ElicitationResponseActionCancel
		actions = append(actions, action)
	}
	return actions, nil
}

// ask asks about one elicitation. It returns an error when ctx ends (Ctrl-C)
// before the person answered.
func (u *urlElicitor) ask(ctx context.Context, elicitation agent.URLElicitation) (mcp.ElicitationResponseAction, error) {
	u.say("\nThis call needs you to open a page:\n%s", describeElicitation(elicitation))
	if _, err := checkElicitationURL(elicitation.URL); err != nil {
		u.say("Declined.\n")
		return mcp.ElicitationResponseActionDecline, nil
	}

	open := u.autoOpen
	for !open {
		answer, err := u.prompt(ctx, "Open it in your browser? [Y/n] ")
		if err != nil {
			return "", err
		}
		switch answer {
		case "", "y", "yes":
			open = true
		case "n", "no":
			u.say("Open the URL yourself.\n")
		default:
			continue
		}
		break
	}
	if open {
		if err := u.open(elicitation.URL); err != nil {
			u.say("Could not open a browser (%v): open the URL yourself.\n", err)
		} else {
			u.say("Opened in your browser.\n")
		}
	}

	for {
		answer, err := u.prompt(ctx, "When you are done on the page, press Enter to continue, or type d to decline or c to cancel: ")
		if err != nil {
			return "", err
		}
		switch answer {
		case "", "y", "yes", "done":
			return mcp.ElicitationResponseActionAccept, nil
		case "d", "decline":
			return mcp.ElicitationResponseActionDecline, nil
		case "c", "cancel":
			return mcp.ElicitationResponseActionCancel, nil
		}
	}
}

// say writes to the terminal; a failed write leaves nothing to tell.
func (u *urlElicitor) say(format string, args ...any) {
	_, _ = fmt.Fprintf(u.out, format, args...)
}

// errNoInput is returned by prompt when the input ended.
var errNoInput = errors.New("no more input")

// prompt writes question and returns the next line, trimmed and lower case.
func (u *urlElicitor) prompt(ctx context.Context, question string) (string, error) {
	u.say("%s", question)
	u.linesOnce.Do(func() {
		u.lines = make(chan string)
		go func() {
			defer close(u.lines)
			scanner := bufio.NewScanner(u.in)
			for scanner.Scan() {
				u.lines <- scanner.Text()
			}
		}()
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line, ok := <-u.lines:
		if !ok {
			return "", errNoInput
		}
		return strings.ToLower(strings.TrimSpace(line)), nil
	}
}
