package project

import (
	"runtime/debug"

	"golang.org/x/mod/module"
)

// dev is the default for unset build identifiers. Local `go build`
// invocations without ldflags keep this so `muster version` stays printable.
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
// toolchain stamped from version control (see Version and GitSHA).
var (
	version        = dev
	gitSHA         = dev
	buildTimestamp = "unknown"
)

// Version returns the best human-readable build identifier available, in
// order: the injected `version` ldflag, the release tag the Go toolchain
// stamped into the build info, the commit SHA, and finally the placeholder
// "dev".
//
// The build info is only trusted when it names a tag. A build of an untagged
// commit gets a pseudo-version derived from the last tag before it,
// v5.23.3-0.<commit time>-<commit> (and while the module path had no /v5
// suffix the toolchain only considered v0 and v1 tags, so even a release
// commit got v1.12.1-0.<commit time>-<commit>). Such a pseudo-version names no
// release and is ignored; releases carry their tag in the `version` ldflag,
// and since the module is github.com/giantswarm/muster/v5 a build of the
// tagged commit carries it in the build info as well.
func Version() string {
	if version != dev && version != "" {
		return version
	}
	if v := readVCS().version; v != "" && !module.IsPseudoVersion(v) {
		return v
	}
	if sha := GitSHA(); sha != dev {
		return sha
	}
	return dev
}

// GitSHA returns the commit SHA the binary was built from: the injected
// `gitSHA` ldflag, else the revision the Go toolchain stamped into the build
// info, else "dev".
func GitSHA() string {
	if gitSHA != dev {
		return gitSHA
	}
	if rev := readVCS().revision; rev != "" {
		return rev
	}
	return dev
}

// BuildTimestamp returns the UTC build time in RFC 3339 format, or
// "unknown" when no ldflag was injected.
func BuildTimestamp() string { return buildTimestamp }

// vcs is what the Go toolchain stamped from version control into the build
// info: the main module's version and the commit. Either is empty when the
// build info has no usable value for it.
type vcs struct {
	version  string
	revision string
}

// readVCS reads the build info. The version is empty for the "(devel)"
// placeholder a build without a resolvable tag produces, so Version can fall
// through to the next source. A variable so tests can stand in a build info.
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
		if s.Key == "vcs.revision" {
			v.revision = s.Value
		}
	}
	return v
}
