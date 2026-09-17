package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingAuthHandler counts the handler calls a 401 may lead to. The
// embedded interface is nil: any other method reached is a test failure.
type recordingAuthHandler struct {
	api.AuthHandler
	logouts, logins, relogins int
	reloginErr                error
}

func (h *recordingAuthHandler) Logout(string) error                 { h.logouts++; return nil }
func (h *recordingAuthHandler) Login(context.Context, string) error { h.logins++; return nil }
func (h *recordingAuthHandler) Relogin(context.Context, string) error {
	h.relogins++
	return h.reloginErr
}

func withAuthHandler(t *testing.T, h api.AuthHandler) {
	t.Helper()
	prev := api.SwapAuthHandler(h)
	t.Cleanup(func() { api.SwapAuthHandler(prev) })
}

const testEndpoint = "https://muster.gazelle.example/mcp"

// Without --login a 401 is answered at once with auth_required: no browser,
// and the stored token is left where it is.
func TestHandleAuthError_FailsFastWithoutLogin(t *testing.T) {
	h := &recordingAuthHandler{}
	withAuthHandler(t, h)
	e := &ToolExecutor{
		options:  ExecutorOptions{AuthMode: AuthModeNone, Context: "gazelle", Quiet: true},
		endpoint: testEndpoint,
	}

	start := time.Now()
	err := e.handleAuthError(context.Background(), errors.New("401"))

	var authRequired *AuthRequiredError
	require.ErrorAs(t, err, &authRequired)
	assert.Equal(t, "gazelle", authRequired.Context)
	assert.True(t, authRequired.CanLogin)
	assert.Contains(t, err.Error(), AuthRequiredMarker)
	assert.Contains(t, err.Error(), "muster auth login --context gazelle")
	assert.Contains(t, err.Error(), "--login")
	assert.Less(t, time.Since(start), time.Second)
	assert.Zero(t, h.logouts, "a 401 must not remove the stored token")
	assert.Zero(t, h.logins+h.relogins, "no browser without --login")
}

// With --login the command signs in again through Relogin, which keeps the
// stored token until the new flow replaces it; Login would take the rejected
// token for a session.
func TestHandleAuthError_LoginSignsInAgainWithoutRemovingTheToken(t *testing.T) {
	h := &recordingAuthHandler{reloginErr: errors.New("browser closed")}
	withAuthHandler(t, h)
	e := &ToolExecutor{
		options:  ExecutorOptions{AuthMode: AuthModeAuto, Quiet: true},
		endpoint: testEndpoint,
	}

	err := e.handleAuthError(context.Background(), errors.New("401"))

	var failed *AuthFailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, 1, h.relogins)
	assert.Zero(t, h.logins)
	assert.Zero(t, h.logouts, "the stored token stays until the new flow replaces it")
}

func TestResolveAuthMode(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		login    bool
		override string
		want     AuthMode
		wantErr  string
	}{
		{name: "default fails fast", want: AuthModeNone},
		{name: "env opts in", env: "auto", want: AuthModeAuto},
		{name: "env prompt", env: "prompt", want: AuthModePrompt},
		{name: "invalid env falls back to the default", env: "browser", want: AuthModeNone},
		{name: "--login opts in", login: true, want: AuthModeAuto},
		{name: "--login wins over the environment", env: "none", login: true, want: AuthModeAuto},
		{name: "--login with --auth auto", login: true, override: "auto", want: AuthModeAuto},
		{name: "--login contradicts --auth none", login: true, override: "none", wantErr: "pass one of them"},
		{name: "--login contradicts --auth prompt", login: true, override: "prompt", wantErr: "pass one of them"},
		{name: "--auth none", override: "none", want: AuthModeNone},
		{name: "--auth auto", override: "auto", want: AuthModeAuto},
		{name: "invalid --auth", override: "browser", wantErr: "invalid auth mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(AuthModeEnvVar, tt.env)
			got, err := ResolveAuthMode(tt.login, tt.override)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCommandFlags_ToExecutorOptions_Login(t *testing.T) {
	t.Setenv(AuthModeEnvVar, "")

	opts, err := (&CommandFlags{OutputFormat: "table"}).ToExecutorOptions()
	require.NoError(t, err)
	assert.Equal(t, AuthModeNone, opts.AuthMode, "no browser by default")

	opts, err = (&CommandFlags{OutputFormat: "table", Login: true}).ToExecutorOptions()
	require.NoError(t, err)
	assert.Equal(t, AuthModeAuto, opts.AuthMode)
}

func TestAuthRequiredError_Message(t *testing.T) {
	byContext := (&AuthRequiredError{Endpoint: testEndpoint, Context: "gazelle", CanLogin: true}).Error()
	assert.True(t, strings.HasPrefix(byContext, AuthRequiredMarker+": "), byContext)
	assert.Contains(t, byContext, "muster auth login --context gazelle")
	assert.NotContains(t, byContext, "--endpoint")
	assert.Contains(t, byContext, "add --login")

	byEndpoint := (&AuthRequiredError{Endpoint: testEndpoint}).Error()
	assert.Contains(t, byEndpoint, "muster auth login --endpoint "+testEndpoint)
	assert.NotContains(t, byEndpoint, "--login")
}
