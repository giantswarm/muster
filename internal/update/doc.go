// Package update keeps the muster binary current. `muster self-update`
// installs the newest GitHub release over the running executable
// (creativeprojects/go-selfupdate against the repository's releases; a
// development build is refused) -- and only after the release's cosign
// Sigstore bundle verifies: architect signs every binary it publishes in
// CircleCI, keyless, and the shared validator
// (github.com/giantswarm/selfupdate-cosign) checks the download against that
// signature before anything is written. A release without a bundle, or a
// download that does not match its bundle, is refused and the installed
// binary stays as it is. Remind prints a one-line hint ahead of every
// user-facing command while a newer release exists -- the per-command check
// devctl runs, with two deliberate differences: it never blocks (an outdated
// muster runs the command the same), and it gives up fast when GitHub cannot
// be reached, so a machine without internet is never held up. GitHub's answer
// is remembered under the user's cache directory for an hour (a failed
// attempt for ten minutes), so the round trip is rare, and capped at two
// seconds when it happens. The hint installs nothing, so it does not ask for
// a bundle.
//
// The package is the same shape as agentlab's internal/update, so the two
// CLIs behave alike; a change here is worth a look there.
package update
