package project

import (
	"runtime/debug"
	"strings"
)

// Name, Module and Source identify the project in messages and reports:
// the binary's name, the Go module (`go install Module@latest` builds the
// newest release) and the repository.
const (
	Name   = "muster"
	Module = "github.com/giantswarm/muster/v5"
	Source = "https://github.com/giantswarm/muster"
)

// dev is what Version reports for a build that carries no version at all: no
// ldflag and no module version in the build info (a `go test` binary, a build
// outside a checkout). Never empty, so a version is always printable.
const dev = "dev"

// devel is the module version runtime/debug reports for a build that carries
// no resolvable VCS tag (no .git, or built outside a module checkout).
const devel = "(devel)"

// Build identifiers, set at link time via `-X` ldflags. Locally the generated
// Makefile.gen.go.mk stamps all three. In CI the architect orb's go-test
// command writes `gitSHA` (from `CIRCLE_SHA1`) and `buildTimestamp` into
// .ldflags, and `make stamp-version` (a prerequisite of `make test`, see
// Makefile.custom.mk) appends `version` -- the release tag on a tag build --
// before the orb's go-build links the binaries with that file. A plain
// `go build` or `go install` sets none of them and falls back to what the Go
// toolchain stamped from version control (see Version, GitSHA and
// BuildTimestamp).
var (
	version        string
	gitSHA         string
	buildTimestamp string
)

// Version returns the muster version in the form the tags use ("v5.23.5"):
// the linker-stamped one when the binary came out of make or the release
// pipeline; else the module version Go recorded, which is the tag for
// `go install …@v5.23.5` and for a `go build` at the tag, a pseudo-version
// between tags ("v5.23.6-0.20260915195350-977012d023ba": after v5.23.5,
// before v5.23.6) and carries "+dirty" over local edits; else "dev".
func Version() string {
	if v := strings.TrimSpace(version); v != "" {
		if v[0] >= '0' && v[0] <= '9' {
			v = "v" + v
		}
		return v
	}
	if v := readVCS().version; v != "" {
		return v
	}
	return dev
}

// GitSHA returns the commit the binary was built from: the linker-stamped
// one, else the revision Go recorded from version control, else "".
func GitSHA() string {
	if gitSHA != "" {
		return gitSHA
	}
	return readVCS().revision
}

// ShortSHA is GitSHA cut to the seven characters git shows, or "" when
// unknown.
func ShortSHA() string {
	sha := GitSHA()
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return sha
}

// BuildTimestamp returns the build time in RFC 3339: the linker-stamped one,
// else the commit time Go recorded from version control, else "".
func BuildTimestamp() string {
	if buildTimestamp != "" {
		return buildTimestamp
	}
	return readVCS().time
}

// VersionLine is what `muster --version` and `muster version` print after the
// name: "v5.23.5 (commit 361cdef, built 2026-09-15T19:38:51Z)", with the
// details the binary knows.
func VersionLine() string {
	line := Version()
	var details []string
	if sha := ShortSHA(); sha != "" {
		details = append(details, "commit "+sha)
	}
	if ts := BuildTimestamp(); ts != "" {
		details = append(details, "built "+ts)
	}
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	return line
}

// vcs is what the Go toolchain stamped from version control into the build
// info: the main module's version, the commit and its time. Each is empty
// when the build info has no usable value for it.
type vcs struct {
	version  string
	revision string
	time     string
}

// readVCS reads the build info. The version is empty for the "(devel)"
// placeholder a build without a resolvable tag produces, so Version can fall
// through to "dev". A variable so tests can stand in a build info.
var readVCS = func() vcs {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return vcs{}
	}
	var v vcs
	if info.Main.Version != devel {
		v.version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			v.revision = s.Value
		case "vcs.time":
			v.time = s.Value
		}
	}
	return v
}
