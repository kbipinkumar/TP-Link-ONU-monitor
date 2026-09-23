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
mkdir -p packaging/debian_build/etc/sudoers.d

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

# Ensure Python scripts are executable
chmod +x packaging/debian_build/opt/onu_monitor/*.py

# systemd units and the narrow sudoers rule that lets the web GUI
# restart the monitor timer without running as root.
cp debian/onu_monitor.service packaging/debian_build/lib/systemd/system/onu_monitor.service
cp debian/onu_monitor.timer packaging/debian_build/lib/systemd/system/onu_monitor.timer
cp debian/onu_monitor_web.service packaging/debian_build/lib/systemd/system/onu_monitor_web.service
cp debian/onu-monitor.sudoers packaging/debian_build/etc/sudoers.d/onu-monitor
chmod 644 packaging/debian_build/lib/systemd/system/onu_monitor.service
chmod 644 packaging/debian_build/lib/systemd/system/onu_monitor.timer
chmod 644 packaging/debian_build/lib/systemd/system/onu_monitor_web.service
chmod 440 packaging/debian_build/etc/sudoers.d/onu-monitor

# Build the package with root ownership
dpkg-deb --root-owner-group --build packaging/debian_build "tp-link-onu-monitor_${VERSION}_all.deb"

echo "Done! Package built as tp-link-onu-monitor_${VERSION}_all.deb"
