# muster context

Manage muster contexts

## Synopsis

Manage named contexts for different muster endpoints.

Contexts provide a convenient way to work with multiple muster aggregator
endpoints without specifying --endpoint for every command. Similar to
kubectl's context management.

Examples:
  muster context                              # List all contexts
  muster context list                         # List all contexts (alias: ls)
  muster context current                      # Show current context
  muster context use production               # Switch to context (alias: switch)
  muster context add staging --endpoint <url> # Add new context
  muster context add staging --endpoint <url> --use  # Add and switch
  muster context update staging --endpoint <url>     # Update context (alias: set)
  muster context delete staging               # Remove a context (alias: rm)
  muster context delete staging --force       # Remove without confirmation
  muster context rename staging stage         # Rename a context
  muster context show production              # Show details (alias: describe)
  muster context show production -o json      # Show as JSON

Context Configuration:
  Contexts are stored in ~/.config/muster/contexts.yaml

Precedence (highest to lowest):
  1. --endpoint flag
  2. --context flag
  3. MUSTER_CONTEXT environment variable
  4. current-context from contexts.yaml
  5. Local fallback (http://localhost:8090/mcp)

```
muster context [flags]
```

## Options

```
  -h, --help    help for context
  -q, --quiet   Suppress non-essential output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
* [muster context add](context-add.md)	 - Add a new context
* [muster context current](context-current.md)	 - Show current context name
* [muster context delete](context-delete.md)	 - Delete a context
* [muster context list](context-list.md)	 - List all contexts
* [muster context rename](context-rename.md)	 - Rename a context
* [muster context show](context-show.md)	 - Show context details
* [muster context update](context-update.md)	 - Update an existing context
* [muster context use](context-use.md)	 - Switch to a different context
