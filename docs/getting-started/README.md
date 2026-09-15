# Getting started

Two tutorials take you from nothing to a muster that aggregates real MCP servers and exposes
them to an agent.

| Tutorial | You end up with |
|---|---|
| [Quick start](quick-start.md) | muster installed and running locally, one MCP server registered, its tools called from the CLI, the REPL and an IDE |
| [Register servers and workflows](platform-setup.md) | MCP servers defined as files, a remote server registered, and a first workflow that turns a multi-step procedure into one tool |

Both tutorials run muster in *filesystem mode*: definitions live as YAML files under
`~/.config/muster`. Running muster as a shared service on Kubernetes, where the same
definitions are custom resources, is covered in [Installation](../operations/installation.md).

When the tutorials are done, the [how-to guides](../how-to/README.md) cover individual tasks and
the [reference](../reference/README.md) has the exact arguments and fields.
