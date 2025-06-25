#!/bin/bash

# Setup script for muster systemd user service
# This script installs and enables the muster systemd service

set -e

echo "🔧 Setting up muster systemd user service..."

# Create systemd user directory if it doesn't exist
mkdir -p ~/.config/systemd/user

# Copy service file
echo "📁 Installing service file..."
cp muster.service ~/.config/systemd/user/

# Reload systemd
echo "🔄 Reloading systemd daemon..."
systemctl --user daemon-reload

# Enable the service
echo "✅ Enabling muster service..."
systemctl --user enable muster.service

echo "📦 Building and installing muster..."
go install .

echo "🚀 Starting muster service..."
systemctl --user start muster.service

echo "📊 Service status:"
systemctl --user status muster.service --no-pager

echo ""
echo "✅ muster systemd service setup complete!"
echo ""
echo "💡 Development workflow:"
echo "  ./scripts/dev-restart.sh                   # Build, install & restart"
echo "  systemctl --user status muster.service     # Check status"
echo "  journalctl --user -u muster.service -f     # Follow logs"
echo "  systemctl --user stop muster.service       # Stop service"
echo "  systemctl --user disable muster.service    # Disable auto-start" 