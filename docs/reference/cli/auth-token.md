# muster auth token

Print the current token for use with other clients

## Synopsis

Print the access token of the current aggregator session, or with --id the
OIDC ID token the identity provider issued at sign-in.

Only the token is written to stdout, so it can be handed to another client
directly:

  curl -H "Authorization: Bearer $(muster auth token --id)" https://models.example.com/v1/models

An expired access token is renewed through the session first. The ID token is
never renewed that way: when it has expired the command fails and names
'muster auth login', which signs in again.

Examples:
  muster auth token                    # Access token of the aggregator session
  muster auth token --id               # OIDC ID token from the sign-in
  muster auth token --id --endpoint <url>

```
muster auth token [flags]
```

## Options

```
  -h, --help   help for token
      --id     Print the OIDC ID token instead of the access token
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
