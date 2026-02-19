# AI Agent Troubleshooting Guide

Diagnose and resolve AI agent issues for reliable infrastructure automation with Muster.

## Quick Diagnostics

### TL;DR Issue Resolution

```bash
# Quick health check
muster status --verbose
muster agent --test-connection
muster list tools --verify

# Common fixes
muster restart
muster cache clear
muster config validate

# Get help
muster troubleshoot --guided
```

## Common Issues and Solutions

### Agent Connection Problems

#### Issue: "No MCP servers available" or "Agent not responding"

**Symptoms:**
- AI agent shows no available tools
- "Connection timeout" errors
- "MCP server not found" messages

**Diagnostic Steps:**
```bash
# 1. Check if Muster is running
ps aux | grep muster
systemctl status muster  # if using systemd

# 2. Test Muster directly
muster version
muster status

# 3. Test agent mode specifically
muster agent --test

# 4. Check configuration
cat ~/.cursor/settings.json | jq '.mcpServers'
# or for other agents
cat ~/.claude/mcp-servers.json | jq '.mcpServers'

# 5. Check logs
muster logs --tail 50
muster logs --component agent
```

**Solutions:**

**Fix 1: Restart Muster**
```bash
# If running standalone mode, restart it
# Kill existing process and restart
muster standalone

# Or if using separate server mode:
muster serve
```

**Fix 2: Reset Configuration**
```bash
# Backup current config
cp ~/.config/muster/config.yaml ~/.config/muster/config.yaml.backup

# Reset to defaults
muster configure reset

# Reconfigure for your setup
muster configure --interactive
```

**Fix 3: Check Network Issues**
```bash
# Test if port is available
netstat -tlnp | grep 3000  # or your configured port

# Check firewall
sudo ufw status
sudo iptables -L

# Test local connection
curl -v http://localhost:3000/health
```

#### Issue: "Authentication failed" or "Unauthorized"

**Symptoms:**
- Tools visible but execution fails
- "401 Unauthorized" errors
- "Invalid credentials" messages

**Diagnostic Steps:**
```bash
# Check authentication status
muster auth status

# Verify credentials
muster auth test

# Check token expiration
muster auth info --show-expiry

# Test with fresh token
muster auth login
```

**Solutions:**

**Fix 1: Re-authenticate**
```bash
# Re-authenticate
muster auth login

# Test authentication
muster auth test
```

**Fix 2: Reset Authentication**
```bash
# Clear stored credentials
muster auth reset

# Re-authenticate
muster auth login --interactive

# Verify authentication works
muster auth test
```

### Tool Discovery and Execution Issues

#### Issue: "Tools not showing up" or "Limited tool availability"

**Symptoms:**
- Expected tools missing from agent
- Only basic tools available
- Tool list incomplete

**Diagnostic Steps:**
```bash
# Check tool discovery
muster list tools --all
muster list tools --available
muster list tools --filtered

# Check MCP server status
muster list mcpserver

# Test specific tool
muster check tool workflow_deploy_webapp

# Check filters
muster config show | grep -A 10 filters
```

**Solutions:**

**Fix 1: Clear Tool Cache**
```bash
# Clear tool cache
muster cache clear --tools

# Reload tools
muster tools reload

# Verify tools available
muster list tools --count
```

**Fix 2: Check MCP Server Status**
```bash
# Check all MCP servers
muster list mcpserver

# Restart specific MCP server
muster restart mcpserver kubernetes-tools

# Check server logs
muster logs mcpserver kubernetes-tools
```

**Fix 3: Review Tool Filters**
```bash
# Check current filters
muster config show filters

# Temporarily disable filters
muster configure filters --disable

# Test tool availability
muster list tools --all

# Re-enable with adjusted filters
muster configure filters --enable
```

#### Issue: "Tool execution fails" or "Tool timeout"

**Symptoms:**
- Tools found but fail to execute
- "Execution timeout" errors
- Partial results or errors

**Diagnostic Steps:**
```bash
# Test tool directly
muster call tool workflow_deploy_webapp \
  --args '{"app_name": "test", "environment": "development"}'

# Check tool health
muster check tool workflow_deploy_webapp --verbose

# Check resource usage
muster metrics --tools
muster metrics --performance

# Review execution logs
muster logs tool workflow_deploy_webapp --recent
```

**Solutions:**

**Fix 1: Increase Timeouts**
```yaml
# ~/.config/muster/config.yaml
timeouts:
  tool_execution: 300s  # 5 minutes
  workflow_execution: 1800s  # 30 minutes
  agent_response: 60s  # 1 minute
```

**Fix 2: Check Resource Limits**
```bash
# Check system resources
free -h
df -h
top -p $(pgrep muster)

# Check Muster limits
muster config show | grep -A 5 limits

# Adjust limits if needed
muster configure limits --memory 2GB --cpu 2
```

**Fix 3: Verify Dependencies**
```bash
# Check tool dependencies
muster check dependencies workflow_deploy_webapp

# Test underlying services
kubectl version  # for Kubernetes tools
docker version   # for Docker tools
terraform version  # for Terraform tools
```

### Performance Issues

#### Issue: "Slow AI responses" or "Agent lag"

**Symptoms:**
- Long delays in agent responses
- Timeouts during conversations
- High CPU/memory usage

**Diagnostic Steps:**
```bash
# Check performance metrics
muster metrics --performance
muster metrics --detailed

# Check system resources
htop
iotop
nethogs

# Profile Muster performance
muster profile --duration 60s

# Check cache efficiency
muster cache stats
```

**Solutions:**

**Fix 1: Optimize Configuration**
```yaml
# ~/.config/muster/config.yaml
performance:
  cache:
    tool_discovery: 600s  # 10 minutes
    result_cache: 300s   # 5 minutes
    max_cache_size: 1GB
    
  concurrency:
    max_parallel_tools: 5
    worker_pool_size: 10
    
  optimization:
    preload_frequent_tools: true
    background_tool_loading: true
    smart_context_management: true
```

**Fix 2: Reduce Context Size**
```yaml
# Limit context in AI configuration
agent:
  context:
    max_files: 10
    max_lines_per_file: 500
    prioritize_recent: true
    exclude_patterns:
      - "*.log"
      - "node_modules/**"
      - ".git/**"
```

**Fix 3: Enable Smart Caching**
```bash
# Enable aggressive caching
muster configure cache --aggressive

# Preload frequently used tools
muster cache preload --popular-tools

# Clean up old cache
muster cache cleanup --older-than 7d
```

#### Issue: "Memory leaks" or "High memory usage"

**Symptoms:**
- Gradually increasing memory usage
- Out of memory errors
- System becomes slow over time

**Diagnostic Steps:**
```bash
# Monitor memory usage over time
watch -n 5 'ps aux | grep muster | grep -v grep'

# Check for memory leaks
muster diagnostics memory --profile 300s

# Review log files for patterns
muster logs --search "memory" --recent 1h

# Check cache sizes
muster cache stats --detailed
```

**Solutions:**

**Fix 1: Restart Periodically**
```bash
# Set up automatic restart
crontab -e
# Add: 0 2 * * * /usr/local/bin/muster restart

# Or use systemd timer
muster configure restart-schedule --daily 2:00
```

**Fix 2: Tune Memory Settings**
```yaml
# ~/.config/muster/config.yaml
memory:
  max_heap_size: 2GB
  garbage_collection: aggressive
  cache_limits:
    max_total_cache: 512MB
    max_tool_cache: 256MB
    max_result_cache: 256MB
```

**Fix 3: Enable Memory Monitoring**
```bash
# Enable memory alerts
muster configure alerts \
  --memory-threshold 80% \
  --action restart \
  --cooldown 1h
```

### Configuration Issues

#### Issue: "Invalid configuration" or "Config errors"

**Symptoms:**
- Muster fails to start
- "Configuration validation failed" errors
- Unexpected behavior

**Diagnostic Steps:**
```bash
# Validate configuration
muster config validate

# Check syntax
muster config check --syntax

# Show effective configuration
muster config show --effective

# Compare with defaults
muster config diff --with-defaults
```

**Solutions:**

**Fix 1: Reset to Known Good Configuration**
```bash
# Backup current config
cp ~/.config/muster/config.yaml ~/.config/muster/config.yaml.broken

# Reset to defaults
muster configure reset

# Apply minimal working config
muster configure --minimal

# Test basic functionality
muster status
```

**Fix 2: Incremental Configuration**
```bash
# Start with minimal config
muster configure reset

# Add components one by one
muster configure add mcpserver kubernetes-tools
muster test

muster configure add workflow deploy-webapp
muster test

# Continue until you find the problematic config
```

**Fix 3: Use Configuration Wizard**
```bash
# Interactive configuration
muster configure --wizard

# Guided setup for specific use case
muster configure --guided --use-case infrastructure

# Validate each step
muster configure --validate-each-step
```

### Environment-Specific Issues

#### Issue: "Production deployment failures"

**Symptoms:**
- Deployments work in dev/staging but fail in production
- Permission denied in production
- Production-specific errors

**Diagnostic Steps:**
```bash
# Check production context
muster check context production

# Verify production credentials
muster auth test --environment production

# Check production-specific configuration
muster config show --environment production

# Test production connectivity
muster test connectivity --environment production
```

**Solutions:**

**Fix 1: Environment Isolation**
```yaml
# ~/.config/muster/config.yaml
environments:
  production:
    safety_level: high
    require_approval: true
    audit_all: true
    restricted_operations:
      - delete
      - scale_down
      - modify_secrets
```

**Fix 2: Production-Specific Authentication**
```bash
# Set up production-specific auth
muster auth configure --environment production \
  --method certificate \
  --cert-path /etc/ssl/muster/prod.crt \
  --key-path /etc/ssl/muster/prod.key

# Test production authentication
muster auth test --environment production
```

#### Issue: "Multi-environment confusion"

**Symptoms:**
- Actions executed in wrong environment
- Context switching problems
- Unexpected environment targeting

**Diagnostic Steps:**
```bash
# Check current context
muster context current

# List available contexts
muster context list

# Check context history
muster context history

# Verify environment mapping
muster config show environments
```

**Solutions:**

**Fix 1: Explicit Environment Specification**
```bash
# Always specify environment in prompts
# Good: "Deploy user-service v1.2.3 to staging environment"
# Bad: "Deploy user-service v1.2.3"

# Configure environment warnings
muster configure warnings --environment-required
```

**Fix 2: Environment Safety Guards**
```yaml
# ~/.config/muster/config.yaml
safety:
  environment_confirmation:
    production: always
    staging: on_destructive_operations
  
  dangerous_operations:
    require_double_confirmation: true
    log_all_attempts: true
```

### Network and Connectivity Issues

#### Issue: "Connection timeouts" or "Network errors"

**Symptoms:**
- Intermittent connection failures
- "Connection refused" errors
- Network timeout errors

**Diagnostic Steps:**
```bash
# Test network connectivity
ping muster-server.company.com
telnet muster-server.company.com 3000

# Check DNS resolution
nslookup muster-server.company.com
dig muster-server.company.com

# Test with different protocols
curl -v http://muster-server.company.com:3000/health
curl -v https://muster-server.company.com:3000/health

# Check proxy settings
echo $HTTP_PROXY
echo $HTTPS_PROXY
```

**Solutions:**

**Fix 1: Configure Network Settings**
```yaml
# ~/.config/muster/config.yaml
network:
  connection_timeout: 30s
  read_timeout: 60s
  retry_attempts: 3
  retry_delay: 5s
  
  proxy:
    http_proxy: "http://proxy.company.com:8080"
    https_proxy: "https://proxy.company.com:8080"
    no_proxy: "localhost,127.0.0.1,.company.com"
```

**Fix 2: Use Connection Pooling**
```yaml
# ~/.config/muster/config.yaml
connection_pool:
  max_connections: 10
  keep_alive: true
  keep_alive_timeout: 300s
  idle_timeout: 60s
```

## Debugging Tools and Techniques

### Enable Debug Mode

```bash
# Enable debug logging
muster configure logging --level debug

# Start with verbose output
muster serve --verbose --debug

# Enable specific debug categories
muster configure debug \
  --categories agent,tools,auth,network

# Save debug logs
muster logs --debug --output /tmp/muster-debug.log
```

### Use Built-in Diagnostics

```bash
# Run full diagnostics
muster diagnostics --comprehensive

# Test specific components
muster diagnostics agent
muster diagnostics mcpservers
muster diagnostics network
muster diagnostics auth

# Generate diagnostic report
muster diagnostics --report --output /tmp/muster-diagnostics.html
```

### Performance Profiling

```bash
# Profile performance
muster profile --duration 120s --output /tmp/muster-profile.json

# Monitor in real-time
muster monitor --real-time

# Analyze performance bottlenecks
muster analyze performance --recent 1h
```

### Advanced Debugging

#### Debug AI Agent Communication

```bash
# Enable MCP debugging
export MCP_DEBUG=1

# Trace agent messages
muster agent --trace-messages

# Log all agent interactions
muster configure agent-logging --all-interactions

# Analyze agent conversation patterns
muster analyze conversations --recent 24h
```

#### Debug Tool Execution

```bash
# Trace tool execution
muster trace tool workflow_deploy_webapp \
  --args '{"app_name": "test", "environment": "dev"}'

# Debug workflow steps
muster debug workflow workflow_deploy_webapp \
  --step-by-step \
  --pause-on-error

# Mock tool execution for testing
muster mock tool workflow_deploy_webapp \
  --simulate-success \
  --response-delay 5s
```

## Recovery Procedures

### Emergency Recovery

```bash
# Emergency mode (minimal functionality)
muster emergency-mode

# Safe mode (disabled dangerous operations)
muster safe-mode

# Recovery mode (attempt automatic fixes)
muster recovery-mode --auto-fix
```

### Backup and Restore

```bash
# Backup configuration
muster backup config --output /backup/muster-config-$(date +%Y%m%d).tar.gz

# Backup workflows
muster backup workflows --output /backup/muster-workflows-$(date +%Y%m%d).tar.gz

# Restore from backup
muster restore --from /backup/muster-config-20240115.tar.gz

# Test after restore
muster test --comprehensive
```

### Reset Procedures

```bash
# Soft reset (clear cache, reload config)
muster reset --soft

# Hard reset (reset to defaults, keep user data)
muster reset --hard

# Factory reset (complete reset, lose all data)
muster reset --factory --confirm-data-loss
```

## Prevention Strategies

### Health Monitoring

```yaml
# ~/.config/muster/config.yaml
monitoring:
  health_checks:
    interval: 30s
    timeout: 10s
    failure_threshold: 3
    
  alerts:
    email: admin@company.com
    slack: "#platform-alerts"
    webhooks:
      - "https://monitoring.company.com/webhook/muster"
      
  metrics:
    enabled: true
    export_interval: 60s
    retention: 30d
```

### Automated Maintenance

```bash
# Set up automated maintenance
muster configure maintenance \
  --schedule "0 2 * * SUN" \
  --tasks "cache-cleanup,log-rotation,health-check"

# Configure automatic updates
muster configure auto-update \
  --channel stable \
  --backup-before-update \
  --test-after-update
```

### Proactive Monitoring

```bash
# Set up monitoring
muster monitor setup \
  --prometheus-endpoint http://prometheus:9090 \
  --grafana-dashboard \
  --alert-manager

# Configure log aggregation
muster logs configure \
  --forwarding elk-stack.company.com:5044 \
  --format json \
  --include-metadata
```

## Getting Additional Help

### Built-in Help

```bash
# Interactive troubleshooting
muster troubleshoot --interactive

# Get help for specific issues
muster help troubleshoot connection-issues
muster help troubleshoot performance-issues

# Generate support bundle
muster support-bundle --output /tmp/muster-support.zip
```

### Community Resources

- **GitHub Issues**: [Report bugs and get help](https://github.com/giantswarm/muster/issues)
- **Documentation**: [Complete troubleshooting reference](../reference/troubleshooting/)
- **Discussions**: [Community troubleshooting forum](https://github.com/giantswarm/muster/discussions)

### Enterprise Support

```bash
# Contact enterprise support
muster support contact \
  --priority high \
  --include-diagnostics \
  --include-logs

# Schedule support session
muster support schedule \
  --type troubleshooting \
  --duration 1h
```

## Related Documentation

- [AI Agent Integration Guide](ai-agent-integration.md)
- [Configuration Reference](../reference/configuration.md)
- [General Troubleshooting](troubleshooting.md) 