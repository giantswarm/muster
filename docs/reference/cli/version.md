# muster version

Print the version number of muster CLI and server

## Synopsis

Displays the muster CLI version -- the release tag, the commit and the build
time the binary knows, on one line like `muster --version` -- and, if the
aggregator server is running, the server version obtained from the MCP protocol
handshake. A binary built from a checkout between releases reports Go's
pseudo-version; one without any version says dev.

```
muster version [flags]
```

## Options

```
  -h, --help   help for version
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
