#!/bin/bash
set -e

# Get version from git tag, fallback to 1.0.0
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "1.0.0")
# Remove leading 'v' if present
VERSION=${VERSION#v}
echo "Detected version: $VERSION"

ARCHITECTURES=("amd64" "arm64")

for ARCH in "${ARCHITECTURES[@]}"; do
    echo "==================================================="
    echo "Building Debian package for Architecture: $ARCH"
    echo "==================================================="

    BUILD_DIR="packaging/debian_build_$ARCH"

    # Setup packaging directory
    rm -rf "$BUILD_DIR"
    mkdir -p "$BUILD_DIR/DEBIAN"
    mkdir -p "$BUILD_DIR/opt/onu_monitor"
    mkdir -p "$BUILD_DIR/lib/systemd/system"

    # Copy Debian control files
    cp debian/control "$BUILD_DIR/DEBIAN/control"
    cp debian/postinst "$BUILD_DIR/DEBIAN/postinst"
    cp debian/prerm "$BUILD_DIR/DEBIAN/prerm"

    # Update DEBIAN/control with the correct version and arch
    sed -i "s/^Version: .*/Version: $VERSION/" "$BUILD_DIR/DEBIAN/control"
    sed -i "s/^Architecture: .*/Architecture: $ARCH/" "$BUILD_DIR/DEBIAN/control"

    # Set standard permissions for DEBIAN hooks
    chmod 755 "$BUILD_DIR/DEBIAN/postinst"
    chmod 755 "$BUILD_DIR/DEBIAN/prerm"

    # Compile the Go binary (optimized for low RAM environments like Raspberry Pi)
    echo "Compiling Go binary for $ARCH..."
    env CGO_ENABLED=0 GOMEMLIMIT=512MiB GOGC=50 GOOS=linux GOARCH=$ARCH go build -p 1 -ldflags="-s -w" -o "$BUILD_DIR/opt/onu_monitor/onu-monitor" ./cmd/onu-monitor

    # Copy resources
    cp onu_config.example.ini "$BUILD_DIR/opt/onu_monitor/"
    cp grafana_dashboard.json "$BUILD_DIR/opt/onu_monitor/"

    # Recreate the systemd files cleanly
    cat << 'EOF' > "$BUILD_DIR/lib/systemd/system/onu_monitor.service"
[Unit]
Description=TP-Link GPON ONU Monitor Service
Wants=network-online.target
After=network-online.target time-sync.target

[Service]
Type=oneshot
User=onu-monitor
Group=onu-monitor
WorkingDirectory=/opt/onu_monitor
ExecStart=/opt/onu_monitor/onu-monitor scrape
EOF

    cat << 'EOF' > "$BUILD_DIR/lib/systemd/system/onu_monitor.timer"
[Unit]
Description=Timer for TP-Link GPON ONU Monitor Service

[Timer]
OnBootSec=5min
OnUnitActiveSec=5min
Unit=onu_monitor.service

[Install]
WantedBy=timers.target
EOF

    cat << 'EOF' > "$BUILD_DIR/lib/systemd/system/onu_monitor_web.service"
[Unit]
Description=TP-Link GPON ONU Monitor - Web Configuration GUI
After=network.target

[Service]
Type=simple
User=onu-monitor
Group=onu-monitor
WorkingDirectory=/opt/onu_monitor
ExecStart=/opt/onu_monitor/onu-monitor web
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

    # Build the package with root ownership
    dpkg-deb --root-owner-group --build "$BUILD_DIR" "tp-link-onu-monitor_${VERSION}_${ARCH}.deb"

    echo "Done! Package built as tp-link-onu-monitor_${VERSION}_${ARCH}.deb"
done
