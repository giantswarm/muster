# muster context add

Add a new context

## Synopsis

Add a new named context pointing to a muster endpoint.

Context names must:
  - Be between 1 and 63 characters
  - Contain only lowercase letters, numbers, and hyphens
  - Start and end with an alphanumeric character

Examples:
  muster context add local --endpoint http://localhost:8090/mcp
  muster context add staging --endpoint https://muster-staging.example.com/mcp
  muster context add production --endpoint https://muster.example.com/mcp --use

```
muster context add <name> --endpoint <url> [flags]
```

## Options

```
      --endpoint string   Endpoint URL for the context (required)
  -h, --help              help for add
      --use               Set as current context after adding
```

## Options inherited from parent commands

```
  -q, --quiet   Suppress non-essential output
```

## SEE ALSO

* [muster context](context.md)	 - Manage muster contexts
