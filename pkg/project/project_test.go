package project

import (
	"runtime/debug"
	"testing"
)

const (
	tag    = "v5.23.5"
	commit = "361cdefdb8c6e2e0e940def05b4b026ee524cff3"
	at     = "2026-09-15T19:38:51Z"
	// pseudo is what Go stamps into a `go build` of the commit after the
	// v5.23.5 tag: after that tag, before v5.23.6.
	pseudo = "v5.23.6-0.20260915195350-977012d023ba"
)

// stub stands in the ldflags and the build info for one test.
func stub(t *testing.T, v, sha, ts string, info vcs) {
	t.Helper()
	origVersion, origSHA, origTS, origReadVCS := version, gitSHA, buildTimestamp, readVCS
	t.Cleanup(func() { version, gitSHA, buildTimestamp, readVCS = origVersion, origSHA, origTS, origReadVCS })
	version, gitSHA, buildTimestamp = v, sha, ts
	readVCS = func() vcs { return info }
}

func TestVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		vcs     vcs
		want    string
	}{
		{name: "nothing known", want: dev},
		{name: "the ldflag wins over the build info", version: "v5.24.0", vcs: vcs{version: tag}, want: "v5.24.0"},
		{name: "an ldflag without the v gets it", version: "5.24.0", want: "v5.24.0"},
		{name: "an ldflag is trimmed", version: " v5.24.0-rc.1 ", want: "v5.24.0-rc.1"},
		{name: "an empty ldflag is unset", version: "", vcs: vcs{version: tag}, want: tag},
		{name: "the build info supplies the tag", vcs: vcs{version: tag}, want: tag},
		{name: "a tag with local edits is reported as such", vcs: vcs{version: tag + "+dirty"}, want: tag + "+dirty"},
		{name: "a go build between tags reports the pseudo-version", vcs: vcs{version: pseudo}, want: pseudo},
		{name: "the commit alone is no version", vcs: vcs{revision: commit}, want: dev},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub(t, tc.version, "", "", tc.vcs)
			if got := Version(); got != tc.want {
				t.Errorf("Version() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommitAndTimeFallBackToTheBuildInfo(t *testing.T) {
	stub(t, "", "", "", vcs{revision: commit, time: at})
	if got := GitSHA(); got != commit {
		t.Errorf("GitSHA() = %q, want the stamped revision", got)
	}
	if got := ShortSHA(); got != "361cdef" {
		t.Errorf("ShortSHA() = %q, want %q", got, "361cdef")
	}
	if got := BuildTimestamp(); got != at {
		t.Errorf("BuildTimestamp() = %q, want the stamped commit time", got)
	}

	stub(t, "", "abc1234", "2026-09-16T00:00:00Z", vcs{revision: commit, time: at})
	if got := GitSHA(); got != "abc1234" {
		t.Errorf("GitSHA() = %q, want the ldflag", got)
	}
	if got := BuildTimestamp(); got != "2026-09-16T00:00:00Z" {
		t.Errorf("BuildTimestamp() = %q, want the ldflag", got)
	}

	stub(t, "", "", "", vcs{})
	if GitSHA() != "" || ShortSHA() != "" || BuildTimestamp() != "" {
		t.Errorf("unknown details must be empty, got %q %q %q", GitSHA(), ShortSHA(), BuildTimestamp())
	}
}

func TestVersionLineCarriesDetailsOnlyWhenKnown(t *testing.T) {
	stub(t, "5.23.5", commit, at, vcs{})
	if got, want := VersionLine(), "v5.23.5 (commit 361cdef, built 2026-09-15T19:38:51Z)"; got != want {
		t.Errorf("VersionLine() = %q, want %q", got, want)
	}
	stub(t, tag, "", "", vcs{})
	if got := VersionLine(); got != tag {
		t.Errorf("VersionLine() = %q, want the bare version", got)
	}
	stub(t, "", "", "", vcs{version: pseudo, revision: "977012d023ba80c44bafeb8807ac8d29b2c747e8"})
	if got, want := VersionLine(), pseudo+" (commit 977012d)"; got != want {
		t.Errorf("VersionLine() = %q, want %q", got, want)
	}
}

// The test binary itself is built from this checkout, so the real readVCS
// sees whatever the toolchain stamped: it must never surface the "(devel)"
// placeholder, and Version must never be empty -- the usage of it in User-Agent
// headers and service.version attributes could not cope.
func TestRealBuildInfo(t *testing.T) {
	if got := readVCS().version; got == devel {
		t.Errorf("readVCS().version = %q, want it hidden", got)
	}
	if Version() == "" {
		t.Error("Version() must never be empty")
	}
}

// Module is what the docs tell people to `go install`; it must be the module
// this binary is built from, or the next major bump silently breaks the
// promise again (the module path had no /v5 suffix while the releases were
// v5.x, and Go resolved `@latest` to the last v1 tag).
func TestModuleIsTheBuiltModule(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info in this test binary")
	}
	if info.Main.Path != Module {
		t.Errorf("project.Module = %q, but this binary is built from %q", Module, info.Main.Path)
	}
}
