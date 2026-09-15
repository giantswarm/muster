# muster context update

Update an existing context

## Synopsis

Update the endpoint or settings of an existing context.

Examples:
  muster context update staging --endpoint https://new-staging.example.com/mcp
  muster context set production --endpoint https://muster.example.com/mcp

```
muster context update <name> --endpoint <url> [flags]
```

## Options

```
      --endpoint string   New endpoint URL for the context (required)
  -h, --help              help for update
```

## Options inherited from parent commands

```
  -q, --quiet   Suppress non-essential output
```

## SEE ALSO

* [muster context](context.md)	 - Manage muster contexts
