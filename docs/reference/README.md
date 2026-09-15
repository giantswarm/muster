# Reference

Exact facts about muster's interfaces. Every page here describes what the current release does;
the reasoning lives in [Explanation](../explanation/README.md).

## Interfaces

| Page | Covers |
|---|---|
| [MCP tools](mcp-tools.md) | The thirteen meta-tools an MCP client sees and the `core_*` tools reachable through `call_tool` |
| [Toolsets](toolsets.md) | The `X-muster-Toolset` header, selector grammar, built-in and configured presets |
| [Configuration](configuration.md) | `config.yaml`, the definition directories, environment variables and the Helm values that set them |
| [Custom resources](crds.md) | `MCPServer`, `Workflow` and `WorkflowExecution` field by field |
| [Events](events.md) | The events muster records for servers and workflows and how to query them |
| [HTTP endpoints](api.md) | The MCP endpoint, health, metrics, the OAuth endpoints and the admin listener |
| [CLI](cli/README.md) | Every `muster` command and flag, rendered from the command tree |

## Cheat sheet

Every command that talks to the aggregator accepts `--endpoint`, `--context` and `--auth`;
`-o json` and `-o yaml` are available wherever a table is printed.

```bash
muster serve                                  # aggregator on http://localhost:8090/mcp
muster standalone                             # aggregator + stdio bridge for an IDE
muster agent --repl                           # explore the catalogue interactively
muster agent --mcp-server                     # stdio bridge to a running muster

muster list mcpserver                         # registered servers and their state
muster get mcpserver <name> -o yaml           # one definition with status
muster create mcpserver <name> --type=streamable-http --url=https://.../mcp --auth-type=oauth
muster check mcpserver <name>                 # is it reachable and healthy
muster start service <name>                   # start / stop / restart a server's connection
muster events --resource-type mcpserver -f    # follow lifecycle events

muster list tool --filter 'x_kubernetes_*'    # aggregated tools by name pattern
muster get tool <name>                        # description and input schema
muster call <tool> --key=value                # call any tool, core or aggregated

muster list workflow                          # workflows and their availability
muster start workflow <name> --arg=value      # run one; prints the execution result
muster get workflow-execution <id>            # the durable record of a run

muster context add prod --endpoint https://muster.example.com/mcp --use
muster auth login                             # browser login to the current context
muster auth status                            # who you are, how long the session lasts
```

## Naming

| Prefix | Meaning |
|---|---|
| `core_*` | muster's own tools: servers, services, workflows, configuration, authentication |
| `x_<server>_*` | tools of the MCP server registered as `<server>` (its `toolPrefix` or, in a family, `x_<family>_*`) |
| `workflow_<name>` | the tool that runs the workflow `<name>` |
