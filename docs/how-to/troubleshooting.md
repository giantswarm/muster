# Troubleshooting Guide

Resolve common Muster issues.

> This guide uses only commands Muster implements. The CLI is `muster serve |
> agent | standalone | list | get | check | call | create | start | stop | auth |
> context | events | test | version | self-update`. There is no `muster status`,
> `muster logs`, `muster describe`, `muster validate`, `muster restart`,
> `muster metrics`, `muster backup`/`restore`, `muster support-bundle`, or
> `muster config` command. Configuration is file-based YAML and most read/inspect
> operations go through `muster get` / `muster list` / `muster call`.

All `list`/`get`/`check`/`call`/`start`/`stop` commands connect to a running
aggregator (`muster serve`) at the configured endpoint (default
`http://localhost:8090/mcp`). Override with `--endpoint` / `MUSTER_ENDPOINT`.

## Debug Tool Discovery Issues

### Issue: Tools not appearing in listings
**Symptoms**: `muster list tools` shows empty or incomplete results.

#### Diagnostic Steps
```bash
# List MCP servers and their status (include unreachable ones with errors)
muster list mcpserver
muster list mcpserver --all --verbose

# Inspect a specific server definition
muster get mcpserver <server-name>

# Check whether a specific server is available
muster check mcpserver <server-name>

# List the tools actually aggregated, optionally filtered
muster list tools
muster list tools --server <server-name>
muster list tools --filter "*<keyword>*"
```

#### Common Causes & Solutions

**1. MCP Server Not Running**

A server that fails to start appears in `muster list mcpserver --all --verbose`
with its error. Fix the command/URL in its definition and restart the aggregator.

**2. Binary Path Issues**
```bash
# Verify the server binary exists, is executable, and runs
which <mcp-server-binary>
ls -la "$(which <mcp-server-binary>)"
<mcp-server-binary> --version
```

**3. Network Connectivity Problems (HTTP/SSE servers)**
```bash
# Check the remote server is listening and reachable
curl -v <server-url>/health
ss -tlnp | grep <port>
```

#### Fix Configuration Issues
```yaml
# A valid MCPServer definition
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
spec:
  description: "Git operations server"
  toolPrefix: "git"
  type: stdio
  autoStart: true
  command: ["npx", "@modelcontextprotocol/server-git"]
  env:
    GIT_ROOT: "/workspace"
```

Apply it as a CRD (`kubectl apply -f`) or create it with
`muster create mcpserver ...`. Definitions also live under
`{config-path}/mcpservers/`.

## Resolve Workflow Failures

### Issue: Workflow execution fails or hangs
**Symptoms**: a workflow ends in a `failed` state, or a step errors with an
unclear message.

#### Diagnostic Steps
```bash
# List recent executions, then inspect one in full
muster list workflow-execution
muster get workflow-execution <execution-id> -o yaml

# Pull out only the failed steps
muster get workflow-execution <execution-id> -o json \
  | jq '.status.steps[] | select(.status == "failed")'
```

The execution object records each step's status, inputs, and result — that is
where step-level detail lives (there is no `muster logs`).

#### Common Workflow Issues

**1. Template Rendering Errors**

The engine renders templates with `missingkey=error`, so a reference to a value
that does not exist fails the step. Reference workflow inputs as
`{{ .input.<arg> }}` and stored step results as `{{ .results.<step-id> }}`.

```bash
# Validate a stored workflow's structure and check its tools are available
muster check workflow <workflow-name>
muster call core_workflow_validate --name <workflow-name>

# Run a workflow, passing arguments
muster start workflow <workflow-name> --environment=staging
muster call workflow_<workflow-name> --json '{"environment":"staging"}'
```

Structural rules (e.g. exactly one of `tool`, `forEach`, or `parallel` per step)
are also enforced by the CRD at `kubectl apply` time.

**2. Tool Not Found Errors**

Confirm the tool a step calls is present with `muster list tools --filter "<name>"`.
A workflow that references an unavailable tool is reported unavailable by
`muster check workflow <name>`.

**3. Dependency Resolution Failures**
```bash
# Inspect a service and the services it depends on
muster get service <service-name> -o json | jq '.spec.dependencies'
muster list service -o wide
```

#### Workflow Debugging Pattern
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: debugged-workflow
  namespace: default
spec:
  description: "Workflow with a guarded recovery step"
  steps:
    - id: problematic_step
      tool: <potentially-failing-tool>
      args:
        input: "{{ .input.user_input }}"
      store: true
      allowFailure: true   # keep going so the recovery step can run

    # Run recovery only when the previous step did not succeed
    - id: conditional_recovery
      tool: <recovery-tool>
      condition:
        fromStep: problematic_step
        expectNot:
          success: true
```

## Fix Service Startup Problems

### Issue: Services fail to start or remain pending
**Symptoms**: service status stuck in `pending`, `starting`, or `failed`.

#### Diagnostic Steps
```bash
# Service status (status detail is in the object itself)
muster get service <service-name> -o yaml

# Check dependencies
muster get service <service-name> -o json | jq '.status.dependencies'
```

#### Common Service Issues

**1. Resource Constraints**
```bash
df -h     # disk
free -h   # memory
top       # CPU

# If muster runs under systemd
systemctl status muster
journalctl -u muster --since "1 hour ago"
```

#### Service Recovery
```bash
# Stop and start the service
muster stop service <service-name>
muster start service <service-name>
```

## Handle Network Connectivity Issues

### Issue: Cannot reach the aggregator
**Symptoms**: connection timeouts, unreachable endpoint, network errors.

#### Diagnostic Steps
```bash
# Health endpoint on the aggregator port (default 8090)
curl -v http://localhost:8090/health

# What is listening?
ss -tlnp | grep 8090

# Is a muster process running?
ps aux | grep '[m]uster'
```

#### Network Configuration Issues

**1. Port Conflicts**
```bash
# Find the process using the aggregator port
sudo lsof -i :8090

# The aggregator port is set in config.yaml. Edit it there, then restart
# the aggregator (muster has no --port flag).
$EDITOR ~/.config/muster/config.yaml
```

**2. Firewall Issues**
```bash
sudo ufw status verbose
sudo iptables -L | grep 8090
```

**3. DNS Resolution Problems**
```bash
nslookup <hostname>
dig <hostname>
# Use the explicit endpoint if name resolution is the problem
muster list tools --endpoint http://<ip>:8090/mcp
```

## Common Error Messages and Solutions

### "Tool not found"
```bash
# Verify the server is up and the tool is registered
muster list mcpserver --all --verbose
muster list tools --filter "<tool-name>"

# To restart a backing MCP server, fix/refresh its definition and restart
# the aggregator; there is no `muster restart`.
```

### "Template rendering failed"
```
# e.g. template: workflow:1:23: executing "workflow" at <.invalid_field>
```
Fix the reference: inputs are `{{ .input.<arg> }}`, results are
`{{ .results.<step-id> }}`. Validate with `muster check workflow <name>`.

### "Permission denied"
```bash
chmod +x /path/to/binary
id "$(whoami)"
```

### "authentication required" (exit code 2) / OAuth failure (exit code 3)
```bash
muster auth status
muster auth login --endpoint <url>
```
Exit codes: `0` success, `1` error, `2` auth required, `3` auth failed.

## Performance Troubleshooting

### Issue: Slow workflow execution
```bash
# Per-step status and timing are recorded on the execution object
muster get workflow-execution <id> -o yaml

# System view while a workflow runs
htop
```

Muster exports logs, traces, and metrics via OpenTelemetry (OTLP). Point the
standard `OTEL_EXPORTER_OTLP_*` environment variables at a collector for real
metrics and traces — there is no `muster metrics`/`muster profile` command.

Speed independent steps up by running them concurrently:
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: optimized-workflow
  namespace: default
spec:
  description: "Run independent steps in parallel"
  steps:
    - id: parallel_group
      parallel:
        - id: task_1
          tool: <independent-task-1>
        - id: task_2
          tool: <independent-task-2>
    - id: combine_results
      tool: <combine-task>
      args:
        input_1: "{{ .results.task_1 }}"
        input_2: "{{ .results.task_2 }}"
```

### Issue: High memory/CPU usage
```bash
ps aux | grep '[m]uster'

# Under systemd, cap resources via a drop-in
sudo systemctl edit muster
```
```ini
[Service]
MemoryMax=4G
CPUQuota=200%
```
```bash
sudo systemctl daemon-reload
sudo systemctl restart muster
```

## System-Level Troubleshooting

### Logs
Muster logs to stderr (and to OTLP if configured). Increase verbosity with
`--debug`; silence the console with `--silent`.
```bash
# Verbose aggregator logs
muster serve --debug > /tmp/muster-debug.log 2>&1

# Under systemd
journalctl -u muster --since "1 hour ago" --follow
```

### Health Checks
```bash
curl -fsS http://localhost:8090/health
muster list service
muster check mcpserver <server-name>
muster check workflow <workflow-name>
```

### Recovery
```bash
# Restart the aggregator cleanly
muster stop      # or Ctrl+C the foreground process
muster serve
```

## Getting Help

### Gathering Debug Information
```bash
muster version
cat ~/.config/muster/config.yaml          # current configuration (it is just a file)
muster list service -o json > services.json
muster list mcpserver --all --verbose -o json > mcpservers.json
muster serve --debug > muster-debug.log 2>&1   # reproduce the issue, then attach the log
```

### Back up / restore configuration
Configuration and entity definitions are plain files under your config directory
(`~/.config/muster/` by default). Back up and restore by copying that directory:
```bash
tar czf muster-config-backup.tar.gz -C ~/.config muster
```

### Community Resources
- **GitHub Issues**: [Report bugs and issues](https://github.com/giantswarm/muster/issues)
- **Discussions**: [Ask questions and share solutions](https://github.com/giantswarm/muster/discussions)

## Related Documentation
- [AI Agent Troubleshooting](ai-troubleshooting.md)
- [Workflow Creation](workflow-creation.md)
- [Configuration Reference](../reference/configuration.md)
