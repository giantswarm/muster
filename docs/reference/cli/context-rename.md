# muster context rename

Rename a context

## Synopsis

Rename an existing context.

If the renamed context was the current context, the current context will be updated.

Examples:
  muster context rename staging stage
  muster context rename prod production

```
muster context rename <old-name> <new-name> [flags]
```

## Options

```
  -h, --help   help for rename
```

## Options inherited from parent commands

```
  -q, --quiet   Suppress non-essential output
```

## SEE ALSO

* [muster context](context.md)	 - Manage muster contexts
