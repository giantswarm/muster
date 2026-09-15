# muster

Aggregate MCP servers behind one authenticated endpoint

## Synopsis

muster aggregates the tools of many MCP servers behind one Model Context
Protocol endpoint. An AI agent connects once and discovers, filters and calls
the tools of every registered server through a small set of meta-tools;
platform teams register servers, control access with toolsets and OAuth,
and run muster locally or as a Kubernetes service.

Start here:
  muster serve                Run the aggregator with the local configuration
  muster standalone           Aggregator and stdio bridge in one process, for an IDE
  muster agent --repl         Explore the aggregated tools interactively
  muster list mcpserver       Show the registered MCP servers

Documentation: https://giantswarm.github.io/muster/

## Options

```
  -h, --help   help for muster
```

## SEE ALSO

* [muster agent](agent.md)	 - MCP Client for the muster aggregator server
* [muster auth](auth.md)	 - Manage authentication for muster
* [muster call](call.md)	 - Call an MCP tool by name
* [muster check](check.md)	 - Check if a resource is available
* [muster completion](completion.md)	 - Generate the autocompletion script for the specified shell
* [muster context](context.md)	 - Manage muster contexts
* [muster create](create.md)	 - Create a resource
* [muster events](events.md)	 - List events for muster resources
* [muster get](get.md)	 - Get detailed information about a resource
* [muster list](list.md)	 - List resources
* [muster self-update](self-update.md)	 - Replace this binary with the latest GitHub release (--check only reports whether one exists)
* [muster serve](serve.md)	 - Start the muster aggregator server.
* [muster standalone](standalone.md)	 - Start the muster in standalone mode
* [muster start](start.md)	 - Start a resource
* [muster stop](stop.md)	 - Stop a resource
* [muster test](test.md)	 - Execute comprehensive behavioral and integration tests for muster
* [muster version](version.md)	 - Print the version number of muster CLI and server
