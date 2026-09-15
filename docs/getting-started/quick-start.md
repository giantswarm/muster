# Quick start

In about ten minutes you install muster, register an MCP server, call its tools from the CLI
and connect an IDE. Everything runs on your machine; nothing needs a cluster or an identity
provider.

## 1. Install

Download the latest release for your platform. Release binaries are signed in CI; later updates
go through `muster self-update`, which verifies the signature before replacing the binary.

```bash
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
curl -fsSL -o muster "https://github.com/giantswarm/muster/releases/latest/download/muster-${os}-${arch}"
chmod +x muster && sudo mv muster /usr/local/bin/
muster version
```

With a Go toolchain installed, `go install github.com/giantswarm/muster@latest` is the
alternative. Other options, including the container image, are in
[Installation](../operations/installation.md).

## 2. Start the aggregator

```bash
muster serve
```

`muster serve` is the aggregator: the long-running process that connects to MCP servers and
serves their tools at `http://localhost:8090/mcp`. On first start it creates
`~/.config/muster` with an empty `mcpservers/` and `workflows/` directory; every definition you
create below lands there as a YAML file. Leave it running and open a second terminal.

## 3. Register an MCP server

Register the reference filesystem server from the MCP project. It is a `stdio` server: muster
starts it as a child process and talks to it over its standard input and output.

```bash
muster create mcpserver files --type=stdio --command=npx \
  --args="-y,@modelcontextprotocol/server-filesystem,$HOME" --autoStart=true
```

The first start downloads the package, so give it a few seconds, then check:

```bash
muster list mcpserver
```

```text
NAME    STATE     TYPE    AUTOSTART
files   Running   stdio   Yes
```

The same definition as a file, which is what `muster create` wrote to
`~/.config/muster/mcpservers/files.yaml`:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: files
spec:
  type: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/you"]
  autoStart: true
```

## 4. Call a tool

The tools of a registered server are aggregated under the prefix `x_<server>_`:

```bash
muster list tool --filter 'x_files_*'
muster get tool x_files_read_text_file
muster call x_files_list_allowed_directories
```

`muster call` works for every tool in the catalogue, including muster's own `core_*` tools
(`muster call core_mcpserver_list`), and prints the result the way an agent would receive it.

## 5. Explore in the REPL

```bash
muster agent --repl
```

The REPL connects to the aggregator as an MCP client. `list tools` shows the catalogue,
`describe tool x_files_read_text_file` the schema of one tool, `call x_files_list_directory
path=/home/you` runs it (arguments as `key=value` or as one JSON object), and `help` lists the
rest. Tab completion works on commands, tool names and argument names.

## 6. Connect an IDE

An MCP client does not see the aggregated tools directly. It sees thirteen *meta-tools*:
`list_tools`, `filter_tools` and `describe_tool` to find a tool, `call_tool` to run it, and
their counterparts for resources and prompts. This is what keeps a catalogue of hundreds of
tools out of the model's context; [MCP tools](../reference/mcp-tools.md) describes each of them.

The simplest way to give an IDE that endpoint is `muster standalone`, which runs the aggregator
and a stdio bridge in one process. With Cursor, add to `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

Stop the `muster serve` from step 2 first; `standalone` starts its own aggregator on the same
port and reads the same `~/.config/muster`. Ask the assistant which tools are available and it
will call `list_tools`; ask it to list your home directory and it will find
`x_files_list_directory` with `filter_tools` and run it with `call_tool`.

To keep a separately running `muster serve`, or to reach a muster running elsewhere, configure
`["agent", "--mcp-server"]` instead. [Connect MCP clients](../how-to/connect-mcp-clients.md)
has the configuration for VS Code, Claude Code, Claude Desktop and clients that connect over
HTTP directly.

## Where to go next

- [Register servers and workflows](platform-setup.md) continues this tutorial with a remote server and a first workflow.
- [Toolsets](../reference/toolsets.md) shows how a client declares the subset of the catalogue it wants to work with.
- [Installation](../operations/installation.md) runs muster on Kubernetes for a team, with login through Dex.
