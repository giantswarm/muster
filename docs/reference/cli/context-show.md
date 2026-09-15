# muster context show

Show context details

## Synopsis

Display detailed information about a specific context.

Supports multiple output formats via --output flag.

Examples:
  muster context show production
  muster context describe staging
  muster context show production --output json
  muster context show production -o yaml

```
muster context show <name> [flags]
```

## Options

```
  -h, --help            help for show
  -o, --output string   Output format (text, json, yaml) (default "text")
```

## Options inherited from parent commands

```
  -q, --quiet   Suppress non-essential output
```

## SEE ALSO

* [muster context](context.md)	 - Manage muster contexts
