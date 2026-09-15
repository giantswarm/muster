# muster self-update

Replace this binary with the latest GitHub release (--check only reports whether one exists)

## Synopsis

Looks up the latest release of giantswarm/muster on GitHub and, when it is
newer than this binary, installs its binary for this OS and architecture over
the running executable. --check only reports both versions (exit status 125
when a newer release exists).

Release binaries are signed in CI (cosign, keyless) and published next to
their Sigstore bundle. The downloaded binary is installed only after that
bundle verifies for a CircleCI build of giantswarm/muster; a release
without a bundle, or a download that does not match its signature, is refused
and the installed binary stays as it is.

A binary without a release version (`muster version` says dev) is refused:
reinstall it from a release or with go install. Every other command prints a
one-line hint on stderr while a newer release is out; MUSTER_NO_UPDATE_CHECK=1
silences it.

```
muster self-update [flags]
```

## Options

```
      --check   report the running and the latest release without installing anything; exit status 125 when a newer one exists
  -h, --help    help for self-update
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
