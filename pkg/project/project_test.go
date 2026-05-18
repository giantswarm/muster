package project

import "testing"

func TestVersionFallback(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		gitSHA         string
		want           string
	}{
		{"no injection", "dev", "dev", "dev"},
		{"goreleaser only", "v1.2.3", "dev", "v1.2.3"},
		{"orb only", "dev", "abc1234", "abc1234"},
		{"both injected; tag wins", "v1.2.3", "abc1234", "v1.2.3"},
	}

	origVersion, origSHA := version, gitSHA
	t.Cleanup(func() { version, gitSHA = origVersion, origSHA })

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.version
			gitSHA = tc.gitSHA
			if got := Version(); got != tc.want {
				t.Errorf("Version() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccessors(t *testing.T) {
	if GitSHA() == "" {
		t.Error("GitSHA must not be empty")
	}
	if BuildTimestamp() == "" {
		t.Error("BuildTimestamp must not be empty")
	}
}
