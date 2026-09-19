#!/bin/bash
set -e

VERSION=${1:-"1.0.0"}
ARCH="amd64"
BUILD_DIR="../../build"

# Make sure binaries exist
if [ ! -f "$BUILD_DIR/invoke-server-linux" ] || [ ! -f "$BUILD_DIR/invoke-app-linux" ]; then
    echo "Linux binaries not found in $BUILD_DIR"
    exit 1
fi

# Build invoke-server
SERVER_DIR="invoke-server_${VERSION}_${ARCH}"
mkdir -p "$SERVER_DIR/DEBIAN"
mkdir -p "$SERVER_DIR/usr/bin"
mkdir -p "$SERVER_DIR/usr/share/invoke"
mkdir -p "$SERVER_DIR/lib/systemd/system"

cat <<EOF > "$SERVER_DIR/DEBIAN/control"
Package: invoke-server
Version: $VERSION
Architecture: $ARCH
Maintainer: Invoke <support@invoke.local>
Description: Invoke HTTP/WebSocket Server
 Browser-based terminal workspace server.
EOF

cp "$BUILD_DIR/invoke-server-linux" "$SERVER_DIR/usr/bin/invoke-server"
chmod +x "$SERVER_DIR/usr/bin/invoke-server"
cp "../../invoke.sh" "$SERVER_DIR/usr/bin/invoke.sh"
cp "invoke-server.service" "$SERVER_DIR/lib/systemd/system/"

dpkg-deb --build "$SERVER_DIR" "$BUILD_DIR/invoke-server.deb"

# Build invoke-app (desktop)
APP_DIR="invoke-desktop_${VERSION}_${ARCH}"
mkdir -p "$APP_DIR/DEBIAN"
mkdir -p "$APP_DIR/usr/bin"
mkdir -p "$APP_DIR/usr/share/invoke"
mkdir -p "$APP_DIR/usr/share/applications"

cat <<EOF > "$APP_DIR/DEBIAN/control"
Package: invoke-desktop
Version: $VERSION
Architecture: $ARCH
Maintainer: Invoke <support@invoke.local>
Description: Invoke Desktop App
 Browser-based terminal workspace launcher.
EOF

cp "$BUILD_DIR/invoke-app-linux" "$APP_DIR/usr/bin/invoke-app"
chmod +x "$APP_DIR/usr/bin/invoke-app"
cp "../../invoke.sh" "$APP_DIR/usr/bin/invoke.sh"
cp "invoke.desktop" "$APP_DIR/usr/share/applications/"

dpkg-deb --build "$APP_DIR" "$BUILD_DIR/invoke-desktop.deb"

# Clean up
rm -rf "$SERVER_DIR" "$APP_DIR"
echo "DEB builds completed."
