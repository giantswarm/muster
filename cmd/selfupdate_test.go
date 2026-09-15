package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/giantswarm/muster/v5/internal/update"
)

// The update itself -- the look-up, the signature check, the replacement, the
// refusals -- is tested in internal/update. These tests cover the cobra shell:
// the command, its flag, its help, and the exit status --check answers with.

func TestNewSelfUpdateCmd(t *testing.T) {
	cmd := newSelfUpdateCmd()
	if cmd.Use != "self-update" {
		t.Errorf("Use = %q, want self-update", cmd.Use)
	}
	if cmd.Short == "" || cmd.Long == "" {
		t.Error("Short and Long must be set: they are the CLI reference")
	}
	if cmd.RunE == nil {
		t.Error("RunE must be set")
	}
	flag := cmd.Flags().Lookup("check")
	if flag == nil {
		t.Fatal("--check flag is missing")
	}
	if flag.DefValue != "false" {
		t.Errorf("--check defaults to %q, want false", flag.DefValue)
	}
}

func TestSelfUpdateCommandHelp(t *testing.T) {
	cmd := newSelfUpdateCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("executing self-update --help: %v", err)
	}
	for _, want := range []string{"Sigstore bundle", "--check", "125", update.OptOutEnv} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestOutdatedIsAnExitStatusNotAnError(t *testing.T) {
	if got := getExitCode(update.ErrOutdated); got != ExitCodeOutdated {
		t.Errorf("getExitCode(ErrOutdated) = %d, want %d", got, ExitCodeOutdated)
	}
	if got := getExitCode(errors.New("looking up the latest release: no route to host")); got != ExitCodeError {
		t.Errorf("getExitCode(other) = %d, want %d", got, ExitCodeError)
	}
}
