#!/bin/bash

# Development restart script for muster
# This script builds, installs, and restarts the muster systemd service

set -e

echo "🔨 Building muster..."
go build -o muster .

echo "📦 Installing muster to $(go env GOPATH)/bin..."
go install .

echo "🔄 Restarting muster socket and service..."
systemctl --user restart muster.socket
# If the service is running, restart it too
if systemctl --user is-active --quiet muster.service; then
    systemctl --user restart muster.service
fi

echo "📊 Checking socket status..."
systemctl --user status muster.socket --no-pager

echo "📊 Checking service status..."
systemctl --user status muster.service --no-pager

echo "📝 Recent logs:"
journalctl --user -u muster.service --no-pager -n 10

echo "✅ muster socket and service restarted successfully!"
echo ""
echo "💡 Useful commands:"
echo "  systemctl --user status muster.socket      # Check socket status"
echo "  systemctl --user status muster.service     # Check service status"
echo "  journalctl --user -u muster.service -f     # Follow logs"
echo "  systemctl --user stop muster.socket        # Stop socket (and service)"
echo "  systemctl --user start muster.socket       # Start socket"
