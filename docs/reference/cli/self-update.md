# muster self-update

Update muster to the latest version

## Synopsis

Checks for the latest release of muster on GitHub and
updates the current binary if a newer version is found.

Release binaries are signed in CI (cosign, keyless) and published next to
their Sigstore bundle. The downloaded binary is installed only after that
bundle verifies for a CircleCI build of giantswarm/muster; a release
without a bundle, or a download that does not match its signature, is refused
and the installed binary stays as it is.

```
muster self-update [flags]
```

## Options

```
  -h, --help   help for self-update
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
