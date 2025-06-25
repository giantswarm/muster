#!/bin/bash

# Development restart script for muster
# This script builds, installs, and restarts the muster systemd service

set -e

echo "🔨 Building muster..."
go build -o muster .

echo "📦 Installing muster to $(go env GOPATH)/bin..."
go install .

echo "🔄 Restarting muster service..."
systemctl --user restart muster.service

echo "📊 Checking service status..."
systemctl --user status muster.service --no-pager

echo "📝 Recent logs:"
journalctl --user -u muster.service --no-pager -n 10

echo "✅ muster restarted successfully!"
echo ""
echo "💡 Useful commands:"
echo "  systemctl --user status muster.service     # Check status"
echo "  journalctl --user -u muster.service -f     # Follow logs"
echo "  systemctl --user stop muster.service       # Stop service"
echo "  systemctl --user start muster.service      # Start service" 