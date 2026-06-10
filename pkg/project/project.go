package project

// dev is the default for unset build identifiers. Local `go build`
// invocations without ldflags keep this so `muster version` stays printable.
const dev = "dev"

// version holds the current release version as a literal so the devctl
// release flow can rewrite it on each release. Build pipelines additionally
// override these at link time via `-X` ldflags:
//
//   - goreleaser (release archives) sets `version` to the semver tag,
//     `gitSHA` to the short commit, `buildTimestamp` to the build date.
//   - architect-orb's `go-build` job (container images) sets `version` via
//     gitsemver, `gitSHA` to `CIRCLE_SHA1`, and `buildTimestamp` to the UTC
//     build time.
var (
	version        = "0.3.12"
	gitSHA         = dev
	buildTimestamp = "unknown"
)

// Version returns the best human-readable build identifier available: the
// injected or literal release version, otherwise the commit SHA if CI
// injected one, otherwise the placeholder "dev".
func Version() string {
	if version != dev && version != "" {
		return version
	}
	if gitSHA != dev {
		return gitSHA
	}
	return dev
}

// GitSHA returns the commit SHA the binary was built from.
func GitSHA() string { return gitSHA }

// BuildTimestamp returns the UTC build time in RFC 3339 format, or
// "unknown" when no ldflag was injected.
func BuildTimestamp() string { return buildTimestamp }
