package project

import "testing"

// pseudo is what Go's VCS stamping gave the v5.22.0 release binary: the module
// path has no /v5 suffix, so the toolchain derived a pseudo-version from the
// last v1 tag instead of using the v5 tag on the commit.
const pseudo = "v1.12.1-0.20260915144925-e6c760a32b48"

func TestVersionFallback(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		gitSHA   string
		vcs      vcs
		want     string
		wantSHA  string
		wantNote string
	}{
		{name: "nothing available", version: "dev", gitSHA: "dev", want: "dev", wantSHA: "dev"},
		{name: "explicit version ldflag wins", version: "v5.24.0", gitSHA: "abc1234", vcs: vcs{version: pseudo, revision: "def5678"}, want: "v5.24.0", wantSHA: "abc1234"},
		{name: "build info supplies a tag", version: "dev", gitSHA: "abc1234", vcs: vcs{version: "v1.2.3"}, want: "v1.2.3", wantSHA: "abc1234"},
		{name: "build info beats sha", version: "dev", gitSHA: "abc1234", vcs: vcs{version: "v1.2.3", revision: "def5678"}, want: "v1.2.3", wantSHA: "abc1234"},
		{name: "build info absent; sha fallback", version: "dev", gitSHA: "abc1234", want: "abc1234", wantSHA: "abc1234"},
		{name: "a pseudo-version names no release; sha fallback", version: "dev", gitSHA: "abc1234", vcs: vcs{version: pseudo}, want: "abc1234", wantSHA: "abc1234"},
		{name: "a dirty pseudo-version names no release either", version: "dev", gitSHA: "abc1234", vcs: vcs{version: pseudo + "+dirty"}, want: "abc1234", wantSHA: "abc1234"},
		{name: "a dirty tag is still that tag", version: "dev", gitSHA: "abc1234", vcs: vcs{version: "v1.2.3+dirty"}, want: "v1.2.3+dirty", wantSHA: "abc1234"},
		{name: "no ldflags at all: the stamped revision stands in for both", version: "dev", gitSHA: "dev", vcs: vcs{version: pseudo, revision: "def5678"}, want: "def5678", wantSHA: "def5678"},
		{name: "empty version ldflag is unset", version: "", gitSHA: "abc1234", want: "abc1234", wantSHA: "abc1234"},
	}

	origVersion, origSHA, origReadVCS := version, gitSHA, readVCS
	t.Cleanup(func() { version, gitSHA, readVCS = origVersion, origSHA, origReadVCS })

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.version
			gitSHA = tc.gitSHA
			readVCS = func() vcs { return tc.vcs }
			if got := Version(); got != tc.want {
				t.Errorf("Version() = %q, want %q", got, tc.want)
			}
			if got := GitSHA(); got != tc.wantSHA {
				t.Errorf("GitSHA() = %q, want %q", got, tc.wantSHA)
			}
		})
	}
}

// The test binary itself is built from this checkout, so the real readVCS sees
// whatever the toolchain stamped: it must never surface the "(devel)"
// placeholder as a version.
func TestReadVCSHidesDevel(t *testing.T) {
	if got := readVCS().version; got == devel {
		t.Errorf("readVCS().version = %q, want it hidden", got)
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
