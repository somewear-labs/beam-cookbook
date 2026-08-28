#!/bin/bash

set -eu

BASE_URL="https://get.somewear.app/atakplugin"
INSTALL_DIR="$HOME/bin"

OS=$(uname -s)
ARCH=$(uname -m)

case "$OS-$ARCH" in
    Darwin-arm64)  BINARY="rpc_darwin_arm64" ;;
    Darwin-x86_64) BINARY="rpc_darwin_amd64" ;;
    Linux-aarch64) BINARY="rpc_linux_arm64" ;;
    Linux-x86_64)  BINARY="rpc_linux_amd64" ;;
    *)
        echo "Unsupported platform: $OS-$ARCH"
        exit 1
        ;;
esac

mkdir -p "$INSTALL_DIR"

echo "Downloading $BINARY..."
curl -fsSL "$BASE_URL/$BINARY" -o "$INSTALL_DIR/rpc"
chmod +x "$INSTALL_DIR/rpc"

echo "Installed rpc to $INSTALL_DIR/rpc"

if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
    echo ""
    echo "Add $INSTALL_DIR to your PATH:"
    echo "  echo 'export PATH=\"\$HOME/bin:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
fi
