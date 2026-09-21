#!/bin/bash

set -eu

BASE_URL="https://get.somewear.app/atakplugin"

OS=$(uname -s)
ARCH=$(uname -m)

case "$OS-$ARCH" in
    Darwin-arm64)  FILE="beam-ops_mac_arm64.zip" ; PLATFORM="mac" ;;
    Darwin-x86_64) FILE="beam-ops_mac_x64.zip"   ; PLATFORM="mac" ;;
    Linux-aarch64) FILE="beam-ops_linux_arm64.AppImage"   ; PLATFORM="linux" ;;
    Linux-x86_64)  FILE="beam-ops_linux_x86_64.AppImage"  ; PLATFORM="linux" ;;
    *)
        echo "Unsupported platform: $OS-$ARCH"
        exit 1
        ;;
esac

echo "Downloading $FILE..."

if [ "$PLATFORM" = "mac" ]; then
    curl -fsSL "$BASE_URL/$FILE" -o /tmp/beam-ops.zip
    unzip -o /tmp/beam-ops.zip "Beam Ops.app" -d /Applications
    rm /tmp/beam-ops.zip
    echo "Installed to /Applications/Beam Ops.app"
else
    INSTALL_DIR="$HOME/bin"
    mkdir -p "$INSTALL_DIR"
    curl -fsSL "$BASE_URL/$FILE" -o "$INSTALL_DIR/beam-ops"
    chmod +x "$INSTALL_DIR/beam-ops"
    echo "Installed beam-ops to $INSTALL_DIR/beam-ops"
    if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
        echo ""
        echo "Add $INSTALL_DIR to your PATH:"
        echo "  echo 'export PATH=\"\$HOME/bin:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
    fi
fi
