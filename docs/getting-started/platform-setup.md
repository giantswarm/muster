# Register servers and workflows

This tutorial continues from the [quick start](quick-start.md). You register MCP servers as
files, add a remote server, and write a workflow that turns a two-step procedure into a single
tool. It takes about fifteen minutes.

## 1. Definitions as files

muster reads its definitions from `~/.config/muster` (or the directory given with
`--config-path`):

```text
~/.config/muster/
├── config.yaml        # aggregator settings; optional, defaults are fine locally
├── mcpservers/        # one MCPServer per file
└── workflows/         # one Workflow per file
```

The files use the same schema as the Kubernetes custom resources, so a definition written here
can later be applied to a cluster unchanged. Add a second `stdio` server by hand:

```yaml
# ~/.config/muster/mcpservers/git.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git
spec:
  description: Git operations on local repositories
  type: stdio
  command: uvx
  args: ["mcp-server-git"]
  autoStart: true
```

The aggregator watches the directory; the server appears without a restart:

```bash
muster list mcpserver
muster check mcpserver git
muster list tool --filter 'x_git_*'
```

If a server does not reach `Running`, `muster get mcpserver git -o yaml` shows the last error in
its status and `muster events --resource-type mcpserver` the sequence of attempts.

## 2. A remote server

Most servers a platform team runs are remote: they are deployed once and reached over HTTP.
Register one with `type: streamable-http` (or `sse` for servers that still use the older
transport):

```yaml
# ~/.config/muster/mcpservers/kubernetes.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
spec:
  description: Kubernetes API of the platform cluster
  type: streamable-http
  url: https://mcp-kubernetes.example.com/mcp
  timeout: 30
```

A server that requires login gets an `auth` block. With `type: oauth`, muster discovers the
server's authorization server and, when a tool of that server is first used, returns a login
URL through `core_auth_login`; the person signs in once in the browser and muster keeps the
grant for them. With `forwardToken: true`, muster instead forwards the identity token of the
person's own muster session, which is single sign-on for servers that trust the same Dex.
[Manage MCP servers](../how-to/mcp-server-management.md) covers both, together with SigV4
signing and request metadata.

## 3. A first workflow

A workflow chains tool calls into one deterministic tool. Where an agent would otherwise
rediscover the same three steps every time, a workflow runs them the same way each time, for a
fraction of the tokens. Save this next to the `files` server from the quick start:

```yaml
# ~/.config/muster/workflows/inspect-directory.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: inspect-directory
spec:
  description: List a directory and read one file from it
  args:
    path:
      type: string
      required: true
      description: Directory to inspect
    file:
      type: string
      required: true
      description: File inside the directory to read
  steps:
    - id: listing
      tool: x_files_list_directory
      args:
        path: "{{ .input.path }}"
      store: true
    - id: content
      tool: x_files_read_text_file
      args:
        path: "{{ .input.path }}/{{ .input.file }}"
      store: true
```

Arguments are declared under `args` and referenced as `{{ .input.<name> }}`; a step with
`store: true` makes its result available to later steps as `{{ .results.<id> }}`. The workflow
becomes the tool `workflow_inspect-directory`:

```bash
muster list workflow
muster check workflow inspect-directory          # are all tools it needs available?
muster start workflow inspect-directory --path=$HOME --file=.bashrc
muster list workflow-execution                   # every run is recorded
```

Conditions, `forEach` loops, `parallel` groups, error handling and output shaping are covered
in [Create workflows](../how-to/workflow-creation.md).

## 4. What an agent sees now

Connect the REPL and look at the catalogue as a client would:

```bash
muster agent --repl
```

`list tools` shows `x_files_*`, `x_git_*`, `x_kubernetes_*` (once it connects),
`workflow_inspect-directory` and muster's `core_*` tools in one namespace. An IDE or agent gets
the same catalogue behind the meta-tools and can be limited to part of it per request with a
[toolset](../reference/toolsets.md), for example `preset:read-only` or `server:kubernetes`.

## Where to go next

- [Manage MCP servers](../how-to/mcp-server-management.md) for authentication, auto-start, health and suspension.
- [Integrate with Kubernetes](../how-to/kubernetes-integration.md) to register mcp-kubernetes with the person's identity forwarded.
- [Installation](../operations/installation.md) to run the same definitions as custom resources on a cluster.
