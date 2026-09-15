# muster auth whoami

Show current authenticated identity

## Synopsis

Show the currently authenticated identity and token information.

This command displays details about your current authentication state,
including the issuer, token expiration, and endpoint information.

Examples:
  muster auth whoami                   # Show identity for configured aggregator
  muster auth whoami --endpoint <url>  # Show identity for specific endpoint

```
muster auth whoami [flags]
```

## Options

```
  -h, --help   help for whoami
```

## Options inherited from parent commands

```
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --endpoint string      Specific endpoint URL to authenticate to
  -q, --quiet                Suppress non-essential output
```

## SEE ALSO

* [muster auth](auth.md)	 - Manage authentication for muster
