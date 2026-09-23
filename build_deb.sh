#!/bin/bash
set -e

echo "Building Debian package..."

# Get version from git tag, fallback to 1.0.0
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "1.0.0")
# Remove leading 'v' if present
VERSION=${VERSION#v}
echo "Detected version: $VERSION"

# Setup packaging directory
rm -rf packaging/debian_build
mkdir -p packaging/debian_build/DEBIAN
mkdir -p packaging/debian_build/opt/onu_monitor/templates
mkdir -p packaging/debian_build/lib/systemd/system

# Copy Debian control files
cp debian/control packaging/debian_build/DEBIAN/control
cp debian/postinst packaging/debian_build/DEBIAN/postinst
cp debian/prerm packaging/debian_build/DEBIAN/prerm

# Update DEBIAN/control with the correct version
sed -i "s/^Version: .*/Version: $VERSION/" packaging/debian_build/DEBIAN/control

# Set standard permissions for DEBIAN hooks
chmod 755 packaging/debian_build/DEBIAN/postinst
chmod 755 packaging/debian_build/DEBIAN/prerm

# Copy python scripts and web UI
cp onu_monitor.py packaging/debian_build/opt/onu_monitor/
cp onu_config.example.ini packaging/debian_build/opt/onu_monitor/
cp grafana_dashboard.json packaging/debian_build/opt/onu_monitor/
cp web_gui/web_gui.py packaging/debian_build/opt/onu_monitor/
cp web_gui/templates/index.html packaging/debian_build/opt/onu_monitor/templates/
cp web_gui/templates/setup.html packaging/debian_build/opt/onu_monitor/templates/

# Ensure Python scripts are executable
chmod +x packaging/debian_build/opt/onu_monitor/*.py

# Recreate the systemd files cleanly
cat << 'EOF' > packaging/debian_build/lib/systemd/system/onu_monitor.service
[Unit]
Description=TP-Link GPON ONU Monitor Service
Wants=network-online.target
After=network-online.target time-sync.target

[Service]
Type=oneshot
User=onu-monitor
Group=onu-monitor
WorkingDirectory=/opt/onu_monitor
ExecStart=/usr/bin/python3 -u /opt/onu_monitor/onu_monitor.py
EOF

cat << 'EOF' > packaging/debian_build/lib/systemd/system/onu_monitor.timer
[Unit]
Description=Timer for TP-Link GPON ONU Monitor Service

[Timer]
OnBootSec=5min
OnUnitActiveSec=5min
Unit=onu_monitor.service

[Install]
WantedBy=timers.target
EOF

cat << 'EOF' > packaging/debian_build/lib/systemd/system/onu_monitor_web.service
[Unit]
Description=TP-Link GPON ONU Monitor - Web Configuration GUI
After=network.target

[Service]
Type=simple
User=onu-monitor
Group=onu-monitor
WorkingDirectory=/opt/onu_monitor
ExecStart=/usr/bin/python3 /opt/onu_monitor/web_gui.py
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

# Build the package with root ownership
dpkg-deb --root-owner-group --build packaging/debian_build "tp-link-onu-monitor_${VERSION}_all.deb"

echo "Done! Package built as tp-link-onu-monitor_${VERSION}_all.deb"
