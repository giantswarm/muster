package project

// dev is the default for unset build identifiers. Local `go build`
// invocations without ldflags keep this so `muster version` stays printable.
const dev = "dev"

// Populated at link time via `-X` ldflags. Two injection paths:
//
//   - goreleaser (release archives) sets `version` to the semver tag,
//     `gitSHA` to the short commit, `buildTimestamp` to the build date.
//   - architect-orb's `go-build` job (container images) sets `gitSHA` to
//     `CIRCLE_SHA1` and `buildTimestamp` to the UTC build time; `version`
//     stays at its default because no tag is plumbed through.
var (
	version        = dev
	gitSHA         = dev
	buildTimestamp = "unknown"
)

// Version returns the best human-readable build identifier available: the
// release tag if goreleaser injected one, otherwise the commit SHA if CI
// injected one, otherwise the placeholder "dev".
func Version() string {
	if version != dev {
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
