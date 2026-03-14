# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Muster

Muster is a universal control plane and meta-MCP (Model Context Protocol) server aggregator for platform engineers and AI agents. It manages multiple backend MCP servers, provides intelligent tool discovery/filtering, includes a mcp-native workflow engine, and exposes its own functionality through mcp tools as well.

## Build & Development Commands

```bash
# Build and install
make build                        # Build local binary
go install                        # Install binary (use after every code change)

# Testing
make test                         # Unit tests with race detector
muster test --scenario <name> --verbose         # Run a single BDD scenario
muster test --scenario <name> --verbose --debug # With debug output
muster test --parallel 50 --base-port 30000     # Run all scenarios

# Code quality (run before every commit)
goimports -w . && go fmt ./...    # Format code
make lint                         # golangci-lint with gosec and goconst
make vet                          # go vet

# Helm
make helm-lint                    # Lint Helm chart
make helm-test                    # Helm unit tests

# Release
make release-dry-run-fast         # Quick release test (linux/amd64 only)
```

## Architecture

### Service Locator Pattern (mandatory)

All inter-package communication goes through `internal/api`. This is the most critical architectural rule:

- `internal/api` defines handler interfaces in `handlers.go` and depends on **no other internal package**
- Each service package creates an `api_adapter.go` implementing the handler interface
- Adapters register themselves via `api.RegisterXxx()` functions
- Consumers retrieve handlers via `api.GetXxx()` — **never import service packages directly**
- **Anti-pattern**: Never import `workflow`, `mcpserver`, `serviceclass`, or `service` packages directly

### Key Packages

- **`cmd/`** — CLI commands (serve, agent, auth, test, create, get, list)
- **`internal/api/`** — Central service locator with handler interfaces and type definitions
- **`internal/app/`** — Application bootstrap, lifecycle, config loading, service registration
- **`internal/aggregator/`** — MCP server aggregation, registry, transports (SSE, stdio, streamable-http)
- **`internal/agent/`** — MCP client, interactive REPL, MCP server mode for AI assistants
- **`internal/mcpserver/`** — MCP server process management and health checking
- **`internal/serviceclass/`** — ServiceClass prerequisites management
- **`internal/workflow/`** — Workflow definitions, execution engine, templating
- **`internal/orchestrator/`** — Service lifecycle orchestration and state machine
- **`internal/testing/`** — BDD test framework, scenarios in `scenarios/*.yaml`
- **`pkg/`** — Public packages (Kubernetes APIs, auth, logging, OAuth)

### Workflow Tool Naming

- Internal/API layer: `action_<workflow-name>`
- Exposed to users: `workflow_<workflow-name>` (aggregator maps between them)

### Entry Points

- `muster serve` → `app.NewApplication()` → initializes all services, starts aggregator
- `muster agent` → REPL, MCP server, or monitoring modes
- `muster test` → BDD scenario test runner

## Conventions

- **Go version**: 1.25.0 (toolchain 1.26.1)
- **Error handling**: Wrap with `fmt.Errorf("context: %w", err)`
- **File size**: Keep under 400 lines, refactor larger files
- **Package docs**: Every package must have a `doc.go`
- **Test coverage**: 80% minimum for new code
- **No flaky tests**: Never use `time.Sleep` in tests. Never use timers to fix race conditions.
- **Schema**: Don't edit `schema.json` manually — generated via `muster test --generate-schema`
- **BDD scenarios are truth**: If a scenario fails, fix the code, not the scenario
- **Tool naming**: `<prefix>_<category>_<action>` (e.g., `x_kubernetes_list_pods`)
- **Exit codes**: 0=success, 1=error, 2=auth required, 3=auth failed

## Config Locations

- Default: `~/.config/muster/config.yaml`
- Project: `./.muster/config.yaml`
- Custom: `--config-path` flag
- Entity definitions: `{configPath}/{workflows,mcpservers,serviceclasses,services}/`
