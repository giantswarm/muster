#!/bin/bash

# Setup script for muster systemd user service
# This script installs and enables the muster systemd service

set -e

echo "🔧 Setting up muster systemd user service..."

# Create systemd user directory if it doesn't exist
mkdir -p ~/.config/systemd/user

# Copy service and socket files
echo "📁 Installing service and socket files..."
cp muster.service ~/.config/systemd/user/
cp muster.socket ~/.config/systemd/user/

# Reload systemd
echo "🔄 Reloading systemd daemon..."
systemctl --user daemon-reload

# Enable the socket for socket activation
echo "✅ Enabling muster socket for socket activation..."
systemctl --user enable muster.socket

echo "📦 Building and installing muster..."
go install .

echo "🚀 Starting muster socket..."
systemctl --user restart muster.socket

echo "📊 Socket status:"
systemctl --user status muster.socket --no-pager

echo "📊 Service status (on-demand via socket activation):"
systemctl --user status muster.service --no-pager

echo ""
echo "✅ muster systemd service with socket activation setup complete!"
echo ""
echo "💡 Development workflow:"
echo "  ./scripts/dev-restart.sh                   # Build, install & restart"
echo "  systemctl --user status muster.socket      # Check socket status"
echo "  systemctl --user status muster.service     # Check service status"
echo "  journalctl --user -u muster.service -f     # Follow logs"
echo "  systemctl --user stop muster.socket        # Stop socket (and service)"
echo "  systemctl --user disable muster.socket     # Disable socket activation"
