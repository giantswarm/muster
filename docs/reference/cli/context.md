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

| Command | Description |
|---------|-------------|
| `list` | List all contexts (default when no subcommand given) |
| `current` | Show current context name |
| `use <name>` | Switch to a different context |
| `add <name> --endpoint <url>` | Add a new context |
| `delete <name>` | Delete a context |
| `rename <old> <new>` | Rename a context |
| `show <name>` | Show context details |

## Examples

### Initial Setup

```bash
# Add your commonly used endpoints
muster context add local --endpoint http://localhost:8090/mcp
muster context add staging --endpoint https://muster-staging.example.com/mcp
muster context add production --endpoint https://muster.example.com/mcp

# Set default context
muster context use staging
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
```

### Show Current Context

```bash
$ muster context current
production
```

### Show Context Details

```bash
$ muster context show production
Name:      production
Endpoint:  https://muster.example.com/mcp
Current:   yes
Settings:
  output:  table
```

### Add a New Context

```bash
$ muster context add development --endpoint https://muster-dev.example.com/mcp
Context "development" added.

To use this context, run:
  muster context use development
```

### Switch Context

```bash
$ muster context use development
Switched to context "development"
```

### Rename a Context

```bash
$ muster context rename development dev
Context "development" renamed to "dev".
```

### Delete a Context

```bash
$ muster context delete dev
Context "dev" deleted.
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

## Shell Completion

Context names support shell completion. After setting up completions for muster, you can tab-complete context names:

```bash
muster context use prod<TAB>
# Completes to: muster context use production
```

## See Also

- [muster auth](auth.md) - Authentication management
- [muster list](list.md) - List resources
- [muster get](get.md) - Get resource details
