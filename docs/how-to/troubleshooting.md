# Troubleshooting Guide

Comprehensive troubleshooting guide for resolving common Muster issues.

## Debug Tool Discovery Issues

### Issue: Tools not appearing in listings
**Symptoms**: `list_tools` shows empty or incomplete results

#### Diagnostic Steps
```bash
# Check MCP server status
muster list mcpserver

# Verify specific server status
muster get mcpserver <server-name>

# Test server availability
muster check mcpserver <server-name>
```

#### Common Causes & Solutions

**1. MCP Server Not Running**
```bash
# Check if server is available
muster get mcpserver <server-name>

# Check server availability
muster check mcpserver <server-name>

# List all servers to see status
muster list mcpserver
```

**2. Binary Path Issues**
```bash
# Verify binary exists
which <mcp-server-binary>

# Check binary permissions
ls -la $(which <mcp-server-binary>)

# Test binary directly
<mcp-server-binary> --version
```

**3. Network Connectivity Problems**
```bash
# Check if server is listening
netstat -tlnp | grep <port>

# Test local connectivity
curl -v http://localhost:<port>/health

# Check firewall rules
sudo iptables -L | grep <port>
```

#### Fix Configuration Issues
```yaml
# Correct MCP server configuration
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

## Resolve Workflow Failures

### Issue: Workflow execution fails or hangs
**Symptoms**: Workflows stuck in "running" state or fail with unclear errors

#### Diagnostic Steps
```bash
# Get workflow execution details
muster get workflow-execution <execution-id>

# Check execution logs
muster logs workflow-execution <execution-id>

# Get step-by-step status
muster describe workflow-execution <execution-id>

# Check for failed steps
muster get workflow-execution <execution-id> -o json | jq '.status.steps[] | select(.status == "failed")'
```

#### Common Workflow Issues

**1. Template Rendering Errors**
```bash
# Check template syntax
muster validate workflow <workflow-file>

# Test with minimal arguments
muster workflow <workflow-name> --dry-run --args '{}'

# Debug template rendering
muster workflow <workflow-name> --debug-templates --args '<test-args>'
```

**2. Tool Not Found Errors**
```yaml
# Verify tool availability before using in workflows
steps:
  - id: verify_tools
    tool: debug_log
    args:
      message: "Available tools: {{.available_tools}}"
  
  - id: main_task
    tool: <required-tool>
    # ... rest of configuration
```

**3. Dependency Resolution Failures**
```bash
# Check service dependencies
muster get service <service-name> -o json | jq '.spec.dependencies'

# Verify dependency services are running
muster list services --filter "status=running"

# Check dependency health
muster describe service <dependency-service>
```

#### Workflow Debugging Patterns
```yaml
# Add debugging to problematic workflows
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: debugged-workflow
  namespace: default
spec:
  name: debugged-workflow
  steps:
    - id: debug_start
      tool: debug_log
      args:
        message: "Starting workflow with: {{.}}"
        level: "info"
        
    - id: problematic_step
      tool: potentially_failing_tool
      args:
        input: "{{.user_input}}"
      store: true
      allowFailure: false  # Fail fast for debugging
      
    - id: debug_result
      tool: debug_log
      args:
        message: "Step result: {{.results.problematic_step}}"
        level: "info"
        
    - id: conditional_recovery
      tool: recovery_action
      condition:
        jsonPath:
          "results.problematic_step.success": false
```

## Fix Service Startup Problems

### Issue: Services fail to start or remain in pending state
**Symptoms**: Service status stuck in "pending", "starting", or "failed"

#### Diagnostic Steps
```bash
# Check service status and events
muster get service <service-name>
muster describe service <service-name>

# Check ServiceClass configuration
muster get serviceclass <class-name>
muster describe serviceclass <class-name>

# Check underlying tool execution
muster logs service <service-name> --step start

# Verify dependencies
muster get service <service-name> -o json | jq '.status.dependencies'
```

#### Common Service Issues

**1. ServiceClass Configuration Errors**
```yaml
# Check for common configuration mistakes
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: fixed-serviceclass
  namespace: default
spec:
  args:
    # Ensure required args are marked correctly
    required_param:
      type: string
      required: true  # Don't forget this
      description: "This parameter is required"
  serviceConfig:
    lifecycleTools:
      start:
        tool: "valid_tool_name"  # Ensure tool exists
        args:
          param: "{{.required_param}}"  # Correct template syntax
        # Add timeout for long-running operations
        timeout: "10m"
      # Always provide a stop tool
      stop:
        tool: "cleanup_tool"
        args:
          service_id: "{{.service_id}}"
```

**2. Dependency Issues**
```bash
# Check if dependencies are healthy
muster list services --dependencies-of <service-name>

# Verify dependency order
muster get serviceclass <class-name> -o json | jq '.spec.serviceConfig.dependencies'

# Check for circular dependencies
muster validate serviceclass <class-file> --check-dependencies
```

**3. Resource Constraints**
```bash
# Check system resources
df -h  # Disk space
free -h  # Memory
top    # CPU usage

# Check Muster resource limits
muster get service <service-name> -o json | jq '.spec.resources'

# Check container/process limits
systemctl status muster
journalctl -u muster --since "1 hour ago"
```

#### Service Recovery Procedures
```bash
# Force restart service
muster restart service <service-name>

# Stop and recreate service
muster stop service <service-name>
muster delete service <service-name>
muster create service <service-name> --serviceClassName <class-name> --args '<args>'

# Check service logs for errors
muster logs service <service-name> --all-steps --since 1h
```

## Handle Network Connectivity Issues

### Issue: Muster components cannot communicate
**Symptoms**: Connection timeouts, unreachable services, network errors

#### Diagnostic Steps
```bash
# Check Muster server status
muster status

# Test connectivity to Muster server
curl -v http://localhost:8080/health

# Check port bindings
netstat -tlnp | grep muster

# Test internal network connectivity
ping <muster-server-host>
telnet <muster-server-host> <port>
```

#### Network Configuration Issues

**1. Port Conflicts**
```bash
# Find process using Muster's port
sudo lsof -i :8080

# Kill conflicting process
sudo kill -9 <pid>

# Change Muster port if needed
muster serve --port 8081
```

**2. Firewall Issues**
```bash
# Check firewall status
sudo ufw status verbose

# Allow Muster ports
sudo ufw allow 8080/tcp
sudo ufw allow 8081/tcp

# For iptables
sudo iptables -A INPUT -p tcp --dport 8080 -j ACCEPT
```

**3. DNS Resolution Problems**
```bash
# Test DNS resolution
nslookup <hostname>
dig <hostname>

# Use IP addresses if DNS fails
muster serve --host 192.168.1.100

# Check /etc/hosts for conflicts
cat /etc/hosts | grep <hostname>
```

#### Network Troubleshooting Tools
```bash
# Network connectivity test
nc -zv <host> <port>

# HTTP connectivity test
curl -I http://<host>:<port>/

# Trace network path
traceroute <host>

# Check network interfaces
ip addr show
ip route show
```

## Common Error Messages and Solutions

### "Tool not found" Errors
```bash
# Error: Tool 'x_example_tool' not found
# Solution: Verify MCP server is running and tool is registered

# Check tool registration
muster agent
list_tools example

# Restart MCP server
muster restart mcpserver <server-name>
```

### "Service dependency timeout" Errors
```yaml
# Error: Dependency service did not become ready within timeout
# Solution: Increase timeout or fix dependency issues

serviceConfig:
  dependencies:
    - name: dependency-service
      serviceClassName: dependency-class
      waitFor: "running"
      timeout: "15m"  # Increase timeout
```

### "Template rendering failed" Errors
```bash
# Error: template: workflow:1:23: executing "workflow" at <.invalid_field>
# Solution: Fix template syntax and variable references

# Check available template variables
muster workflow <name> --show-template-vars

# Validate template syntax
muster validate workflow <file>
```

### "Permission denied" Errors
```bash
# Error: Permission denied when accessing files/resources
# Solution: Check file permissions and user privileges

# Fix file permissions
chmod +x /path/to/binary
chown muster:muster /path/to/config

# Check user permissions
id muster
groups muster
```

## Performance Troubleshooting

### Issue: Slow workflow execution
**Symptoms**: Workflows taking much longer than expected

#### Diagnostic Steps
```bash
# Check workflow execution timeline
muster get workflow-execution <id> --show-timeline

# Monitor resource usage during execution
htop
iotop
nethogs

# Check for resource bottlenecks
muster metrics --workflow <execution-id>
```

#### Performance Optimization
```yaml
# Optimize workflow with parallel execution
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: optimized-workflow
  namespace: default
spec:
  name: optimized-workflow
  steps:
    # Run independent steps in parallel
    - id: parallel_group
      parallel:
        - id: task_1
          tool: independent_task_1
        - id: task_2
          tool: independent_task_2
        - id: task_3
          tool: independent_task_3
    
    # Continue with dependent steps
    - id: combine_results
      tool: combine_task
      args:
        input_1: "{{.results.task_1}}"
        input_2: "{{.results.task_2}}"
        input_3: "{{.results.task_3}}"
```

### Issue: High memory/CPU usage
**Symptoms**: System slowdown, out of memory errors

#### Solutions
```bash
# Monitor Muster resource usage
ps aux | grep muster
systemctl status muster

# Adjust resource limits
# Edit systemd service file
sudo systemctl edit muster
```

```ini
[Service]
MemoryLimit=4G
CPUQuota=200%
```

```bash
# Restart with new limits
sudo systemctl daemon-reload
sudo systemctl restart muster
```

## System-Level Troubleshooting

### Log Analysis
```bash
# System logs
journalctl -u muster --since "1 hour ago" --follow

# Application logs
tail -f /var/log/muster/muster.log

# Workflow execution logs
muster logs workflow-execution <id> --step <step-id>

# Service logs
muster logs service <service-name> --all-steps
```

### Health Checks
```bash
# Overall system health
muster status --verbose

# Component health
muster check mcpservers
muster check services
muster check workflows

# Storage health
df -h /var/lib/muster
du -sh /var/lib/muster/*
```

### Recovery Procedures
```bash
# Restart Muster cleanly
muster stop
sleep 5
muster start

# Reset to clean state (CAUTION: destroys data)
muster stop
rm -rf /var/lib/muster/data/*
muster start

# Backup and restore
muster backup --output backup.tar.gz
muster restore --input backup.tar.gz
```

## Getting Help

### Gathering Debug Information
```bash
# Create support bundle
muster support-bundle --output muster-debug.tar.gz

# Export system information
muster version --detailed > system-info.txt
muster config show > current-config.yaml
muster list services --output json > services.json
```

### Enable Debug Logging
```yaml
# Enable debug logging in configuration
apiVersion: muster.giantswarm.io/v1alpha1
kind: Config
metadata:
  name: debug-config
spec:
  logging:
    level: debug
    format: json
    outputs:
      - type: file
        path: /var/log/muster/debug.log
      - type: console
```

### Community Resources
- **GitHub Issues**: [Report bugs and issues](https://github.com/giantswarm/muster/issues)
- **Discussions**: [Ask questions and share solutions](https://github.com/giantswarm/muster/discussions)
- **Documentation**: [Latest documentation](https://github.com/giantswarm/muster/docs)

## Related Documentation
- [Configuration Reference](../reference/configuration.md) 
