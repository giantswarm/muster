# Installation Guide

Complete guide for deploying Muster in production environments.

## Overview

Muster can be deployed in several configurations depending on your needs:
- **Standalone mode**: Single process for development and small teams
- **Server mode**: Separate aggregator server for production use
- **Kubernetes deployment**: Container-based deployment with CRDs

## Prerequisites

### System Requirements
- Go 1.21+ (for building from source)
- Linux, macOS, or Windows
- 512MB RAM minimum, 1GB recommended
- 100MB disk space

### Network Requirements
- Port 8080 (default) for HTTP API
- Port 8081 (default) for MCP protocol
- Outbound internet access for MCP server communications

## Installation Methods

### Method 1: Homebrew (macOS - Recommended)

The easiest way to install Muster on macOS is via Homebrew:

```bash
# Add the Muster tap
brew tap giantswarm/muster

# Install Muster
brew install muster
```

#### Upgrade
```bash
brew upgrade muster
```

#### Verify Installation
```bash
muster version
```

### Method 2: Binary Installation

#### Download Latest Release
```bash
# Linux (x86_64)
curl -L https://github.com/giantswarm/muster/releases/latest/download/muster-linux-amd64 -o muster
chmod +x muster
sudo mv muster /usr/local/bin/

# macOS (x86_64)
curl -L https://github.com/giantswarm/muster/releases/latest/download/muster-darwin-amd64 -o muster
chmod +x muster
sudo mv muster /usr/local/bin/

# macOS (ARM64)
curl -L https://github.com/giantswarm/muster/releases/latest/download/muster-darwin-arm64 -o muster
chmod +x muster
sudo mv muster /usr/local/bin/
```

#### Verify Installation
```bash
muster version
```

### Method 3: Build from Source

```bash
# Clone repository
git clone https://github.com/giantswarm/muster.git
cd muster

# Build binary
go build -o muster .

# Install globally
sudo mv muster /usr/local/bin/
```

### Method 4: Container Deployment

```bash
# Run with Docker
docker run -p 8080:8080 -p 8081:8081 \
  -v ~/.config/muster:/config \
  giantswarm/muster:latest serve --config-path=/config
```

## Deployment Configurations

### Standalone Deployment

Perfect for development, local use, and small teams.

```bash
# Start in standalone mode
muster standalone --port 8080
```

**Features:**
- Single process handles both server and agent functionality
- Automatic configuration
- Minimal resource usage
- Ideal for IDE integration

### Server Deployment

Recommended for production environments with multiple clients.

```bash
# Start the aggregator server
muster serve --port 8080 --mcp-port 8081

# Connect agents (separate terminals/machines)
muster agent --endpoint http://your-server:8080 --mcp-server
```

**Features:**
- Separate server and agent processes
- Multiple client support
- Better monitoring and logging
- Horizontal scaling capabilities

### Kubernetes Deployment

For container orchestration environments.

#### Install CRDs
```bash
kubectl apply -f https://github.com/giantswarm/muster/releases/latest/download/crds.yaml
```

#### Deploy Muster Server
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: muster-server
  namespace: muster-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: muster-server
  template:
    metadata:
      labels:
        app: muster-server
    spec:
      containers:
      - name: muster
        image: giantswarm/muster:latest
        command: ["muster", "serve"]
        ports:
        - containerPort: 8080
          name: http
        - containerPort: 8081
          name: mcp
        env:
        - name: MUSTER_CONFIG_PATH
          value: "/config"
        volumeMounts:
        - name: config
          mountPath: /config
      volumes:
      - name: config
        configMap:
          name: muster-config
---
apiVersion: v1
kind: Service
metadata:
  name: muster-service
  namespace: muster-system
spec:
  selector:
    app: muster-server
  ports:
  - name: http
    port: 8080
    targetPort: 8080
  - name: mcp
    port: 8081
    targetPort: 8081
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MUSTER_CONFIG_PATH` | Configuration directory | `~/.config/muster` |
| `MUSTER_LOG_LEVEL` | Logging level (debug\|info\|warn\|error) | `info` |
| `MUSTER_HTTP_PORT` | HTTP API port | `8080` |
| `MUSTER_MCP_PORT` | MCP protocol port | `8081` |

### Configuration Directory Structure

```
~/.config/muster/
├── config.yaml           # Main configuration
├── mcpservers/           # MCP server definitions
│   ├── kubernetes.yaml
│   └── prometheus.yaml
├── serviceclasses/       # Service templates
│   └── web-app.yaml
└── workflows/           # Workflow definitions
    └── deploy-app.yaml
```

### Basic Configuration
```yaml
# ~/.config/muster/config.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Configuration
metadata:
  name: default
spec:
  server:
    port: 8080
    mcpPort: 8081
  logging:
    level: info
  aggregator:
    enableMetrics: true
    toolTimeout: 30s
```

## Post-Installation Setup

### 1. Verify Installation
```bash
# Check version
muster version

# Test server startup
muster serve --dry-run
```

### 2. Initial Configuration
```bash
# Create default configuration
muster create config --default

# List available commands
muster --help
```

### 3. Set Up Your First MCP Server
```bash
# Create a basic MCP server configuration
muster create mcpserver kubernetes \
  --command="kubectl" \
  --args="mcp-server" \
  --auto-start=true
```

### 4. Test the Setup
```bash
# Start server
muster serve

# In another terminal, test agent connection
muster agent --repl
```

## Service Management

### Systemd Service (Linux)

Create a systemd service for automatic startup:

```bash
# Create service file
sudo tee /etc/systemd/system/muster.service > /dev/null <<EOF
[Unit]
Description=Muster Aggregator Server
After=network.target

[Service]
Type=simple
User=muster
Group=muster
ExecStart=/usr/local/bin/muster serve
Restart=always
RestartSec=10
Environment=MUSTER_CONFIG_PATH=/etc/muster

[Install]
WantedBy=multi-user.target
EOF

# Create muster user
sudo useradd -r -s /bin/false muster
sudo mkdir -p /etc/muster
sudo chown muster:muster /etc/muster

# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable muster
sudo systemctl start muster
```

### Process Manager (macOS/Linux)

Using PM2 or similar process managers:

```bash
# Install PM2
npm install -g pm2

# Create process configuration
cat > muster.json <<EOF
{
  "name": "muster",
  "script": "/usr/local/bin/muster",
  "args": ["serve"],
  "instances": 1,
  "autorestart": true,
  "watch": false,
  "env": {
    "MUSTER_CONFIG_PATH": "/home/user/.config/muster"
  }
}
EOF

# Start with PM2
pm2 start muster.json
pm2 save
pm2 startup
```

## Security Considerations

### Network Security
- Run behind a reverse proxy (nginx, Traefik) in production
- Enable TLS/SSL for external communications
- Use firewall rules to restrict access
- Consider VPN or private networks for sensitive environments

### Access Control
- Implement authentication at the reverse proxy level
- Use service accounts for Kubernetes deployments
- Rotate any API keys or secrets regularly
- Monitor access logs for unusual activity

### Configuration Security
- Store sensitive configuration in secrets management systems
- Use environment variables for runtime secrets
- Restrict file permissions on configuration directories
- Regular security updates and patches

## Troubleshooting

### Common Issues

#### Port Already in Use
```bash
# Check what's using the port
sudo lsof -i :8080
sudo lsof -i :8081

# Use alternative ports
muster serve --port 8082 --mcp-port 8083
```

#### Permission Denied
```bash
# Fix binary permissions
chmod +x /usr/local/bin/muster

# Fix config directory permissions
mkdir -p ~/.config/muster
chmod 755 ~/.config/muster
```

#### Configuration Not Found
```bash
# Create default configuration
muster create config --default

# Specify custom config path
muster serve --config-path /path/to/config
```

### Logs and Debugging

```bash
# Enable debug logging
muster serve --log-level debug

# Check system logs (systemd)
sudo journalctl -u muster -f

# Check application logs
tail -f ~/.config/muster/logs/muster.log
```

## Upgrading

### Binary Upgrade
```bash
# Download new version
curl -L https://github.com/giantswarm/muster/releases/latest/download/muster-linux-amd64 -o muster-new

# Stop service
sudo systemctl stop muster

# Replace binary
sudo mv muster-new /usr/local/bin/muster
sudo chmod +x /usr/local/bin/muster

# Start service
sudo systemctl start muster
```

### Kubernetes Upgrade
```bash
# Update CRDs
kubectl apply -f https://github.com/giantswarm/muster/releases/latest/download/crds.yaml

# Update deployment image
kubectl set image deployment/muster-server muster=giantswarm/muster:latest -n muster-system
```

## Next Steps

After installation:
1. [Configure your first MCP server](../how-to/mcp-server-management.md)
2. [Create service classes](../how-to/serviceclass-patterns.md)
3. [Build your first workflow](../how-to/workflow-creation.md)
4. [Set up monitoring](monitoring.md)

## Support

- [Troubleshooting Guide](../how-to/troubleshooting.md)
- [GitHub Issues](https://github.com/giantswarm/muster/issues)
- [GitHub Discussions](https://github.com/giantswarm/muster/discussions) 