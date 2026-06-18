package project

import "testing"

func TestVersionFallback(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		buildInfo string
		gitSHA    string
		want      string
	}{
		{"nothing available", "dev", "", "dev", "dev"},
		{"explicit version ldflag wins", "v1.2.3", "v0.9.0", "abc1234", "v1.2.3"},
		{"build info supplies version", "dev", "v1.2.3", "abc1234", "v1.2.3"},
		{"build info absent; sha fallback", "dev", "", "abc1234", "abc1234"},
		{"build info beats sha", "dev", "v1.2.3", "abc1234", "v1.2.3"},
	}

	origVersion, origSHA, origBuildInfo := version, gitSHA, buildInfoVersion
	t.Cleanup(func() { version, gitSHA, buildInfoVersion = origVersion, origSHA, origBuildInfo })

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.version
			gitSHA = tc.gitSHA
			buildInfoVersion = func() string { return tc.buildInfo }
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
