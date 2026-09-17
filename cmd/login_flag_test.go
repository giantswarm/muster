package cmd

import "testing"

// Every command that connects to an aggregator opts into the browser sign-in
// with --login; without it a missing token is an auth_required failure.
func TestLoginFlagIsOptIn(t *testing.T) {
	defaults := map[string]func() string{
		"call": func() string {
			if f := callCmd.PersistentFlags().Lookup("login"); f != nil {
				return f.DefValue
			}
			return ""
		},
		"agent": func() string {
			if f := agentCmd.Flags().Lookup("login"); f != nil {
				return f.DefValue
			}
			return ""
		},
	}
	for name, defValue := range defaults {
		switch got := defValue(); got {
		case "":
			t.Errorf("%s: no --login flag", name)
		case "false":
		default:
			t.Errorf("%s: --login defaults to %q, want false", name, got)
		}
	}
}
