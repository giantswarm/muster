# muster context

Manage named contexts for different muster endpoints.

## Synopsis

```bash
muster context [command]
```

## Description

Contexts provide a convenient way to work with multiple muster aggregator endpoints without specifying `--endpoint` for every command. Similar to kubectl's context management, you can define named contexts pointing to different muster servers and quickly switch between them.

### Configuration File

Contexts are stored in `~/.config/muster/contexts.yaml`:

```yaml
current-context: production
contexts:
  - name: local
    endpoint: http://localhost:8090/mcp
  - name: staging
    endpoint: https://muster-staging.example.com/mcp
  - name: production
    endpoint: https://muster.example.com/mcp
    settings:
      output: table
```

### Endpoint Resolution Precedence

When determining which endpoint to use, muster checks in this order (highest to lowest priority):

1. `--endpoint` flag - for one-off connections
2. `--context` flag - temporary context override
3. `MUSTER_CONTEXT` environment variable
4. `current-context` from `contexts.yaml`
5. Local fallback via config file

## Commands

| Command | Aliases | Description |
|---------|---------|-------------|
| `list` | `ls` | List all contexts (default when no subcommand given) |
| `current` | | Show current context name |
| `use <name>` | `switch` | Switch to a different context |
| `add <name> --endpoint <url>` | | Add a new context |
| `update <name> --endpoint <url>` | `set` | Update an existing context's endpoint |
| `delete <name>` | `rm`, `remove` | Delete a context (requires confirmation) |
| `rename <old> <new>` | | Rename a context |
| `show <name>` | `describe`, `get` | Show context details |

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--quiet` | `-q` | Suppress non-essential output |

## Examples

### Initial Setup

```bash
# Add your commonly used endpoints
muster context add local --endpoint http://localhost:8090/mcp
muster context add staging --endpoint https://muster-staging.example.com/mcp
muster context add production --endpoint https://muster.example.com/mcp

# Set default context
muster context use staging

# Or add and switch in one command
muster context add production --endpoint https://muster.example.com/mcp --use
```

### Daily Usage

```bash
# Work with staging (current context)
muster list service
muster auth login
muster get workflow my-workflow

# Quick check on production without switching
muster list service --context production

# Switch to production for extended work
muster context use production
muster list service
```

### List All Contexts

```bash
$ muster context list
CURRENT  NAME        ENDPOINT
*        production  https://muster.example.com/mcp
         staging     https://muster-staging.example.com/mcp
         local       http://localhost:8090/mcp

# Short form
$ muster context ls
```

### Show Current Context

```bash
$ muster context current
production
```

### Show Context Details

```bash
$ muster context show production
Name:     production
Endpoint: https://muster.example.com/mcp
Current:  yes
Settings:
  output: table

# JSON output for scripting
$ muster context show production -o json
{
  "name": "production",
  "endpoint": "https://muster.example.com/mcp",
  "current": true,
  "settings": {
    "output": "table"
  }
}

# YAML output
$ muster context show production -o yaml
```

### Add a New Context

```bash
$ muster context add development --endpoint https://muster-dev.example.com/mcp
Context "development" added.

To use this context, run:
  muster context use development

# Add and switch immediately
$ muster context add development --endpoint https://muster-dev.example.com/mcp --use
Context "development" added.
Switched to context "development"
```

### Update an Existing Context

```bash
$ muster context update staging --endpoint https://new-staging.example.com/mcp
Context "staging" updated.

# Using the 'set' alias
$ muster context set staging --endpoint https://new-staging.example.com/mcp
```

### Switch Context

```bash
$ muster context use development
Switched to context "development"

# Using the 'switch' alias
$ muster context switch production
Switched to context "production"
```

### Rename a Context

```bash
$ muster context rename development dev
Context "development" renamed to "dev".
```

### Delete a Context

```bash
# With confirmation prompt
$ muster context delete dev
Delete context "dev"? [y/N] y
Context "dev" deleted.

# Skip confirmation with --force
$ muster context delete dev --force
Context "dev" deleted.

# Short form
$ muster context rm dev -f
```

## Context Name Rules

Context names must:
- Be between 1 and 63 characters
- Contain only lowercase letters, numbers, and hyphens
- Start and end with an alphanumeric character

Valid examples: `local`, `my-prod`, `cluster-01`

Invalid examples: `My_Context`, `-invalid`, `has spaces`

## Environment Variable

You can override the current context using the `MUSTER_CONTEXT` environment variable:

```bash
# Use staging context for this command only
MUSTER_CONTEXT=staging muster list service

# Export for the entire shell session
export MUSTER_CONTEXT=production
```

## Using Context with Commands

All muster commands that connect to an aggregator support the `--context` flag:

```bash
# Use production context for this command
muster list service --context production

# The --endpoint flag still takes precedence
muster list service --endpoint https://custom.example.com/mcp
```

## Integration with Authentication

Contexts work seamlessly with muster's authentication system. Authentication tokens are stored per-endpoint, so switching contexts will automatically use the correct token:

```bash
# Login to production
muster context use production
muster auth login

# Login to staging
muster context use staging
muster auth login

# Now both endpoints have tokens stored
# Switching context uses the correct token automatically
```

## Quiet Mode for Scripting

Use `-q` or `--quiet` for cleaner scripting:

```bash
# Silent context switching
muster context use production -q

# Silent add and switch
muster context add staging --endpoint https://staging.example.com/mcp --use -q

# Combine with other scripted operations
muster context use production -q && muster list service
```

## Shell Completion

Context names support shell completion. After setting up completions for muster, you can tab-complete context names:

```bash
muster context use prod<TAB>
# Completes to: muster context use production
```

## REPL Context Switching

When using `muster agent --repl`, you can also switch contexts interactively without leaving the REPL:

```bash
𝗺 local » context list
Available contexts:
* local
  staging
  production

𝗺 local » context use staging
Switched to context "staging"
Endpoint: https://muster-staging.example.com/mcp

Reconnecting to new endpoint: https://muster-staging.example.com/mcp
Connected to https://muster-staging.example.com/mcp

𝗺 staging » 
```

The REPL automatically reconnects to the new endpoint when you switch contexts. The current context is displayed in the prompt, along with an `[auth required]` indicator if any servers need authentication.

Use `ctx` as a shorthand alias for `context`.

## See Also

- [muster agent](agent.md) - Interactive REPL mode with context switching
- [muster auth](auth.md) - Authentication management
- [muster list](list.md) - List resources
- [muster get](get.md) - Get resource details
