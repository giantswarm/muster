# muster context delete

Delete a context

## Synopsis

Remove a context by name.

If the deleted context was the current context, the current context will be cleared.

By default, this command asks for confirmation. Use --force to skip the prompt.

Examples:
  muster context delete staging
  muster context delete staging --force
  muster context rm staging -f

```
muster context delete <name> [flags]
```

## Options

```
  -f, --force   Skip confirmation prompt
  -h, --help    help for delete
```

## Options inherited from parent commands

```
  -q, --quiet   Suppress non-essential output
```

## SEE ALSO

* [muster context](context.md)	 - Manage muster contexts
