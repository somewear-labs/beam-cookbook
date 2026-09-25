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

# Use a system PATH location when running as root (e.g. via install-somewear.sh),
# otherwise fall back to ~/bin and add it to the shell profile.
if [ "$EUID" -eq 0 ]; then
    LAUNCHER_DIR="/usr/local/bin"
    REAL_USER="${SUDO_USER:-$USER}"
    REAL_HOME=$(eval echo "~$REAL_USER")
else
    LAUNCHER_DIR="$HOME/bin"
    REAL_HOME="$HOME"
fi
mkdir -p "$LAUNCHER_DIR"

echo "Downloading $FILE..."

if [ "$PLATFORM" = "mac" ]; then
    curl -fsSL "$BASE_URL/$FILE" -o /tmp/beam-ops.zip
    unzip -o /tmp/beam-ops.zip -d /Applications > /dev/null 2>&1; true
    rm /tmp/beam-ops.zip
    echo "Installed to /Applications/Beam Ops.app"

    cat > "$LAUNCHER_DIR/beam-ops" << 'EOF'
#!/bin/sh
open "/Applications/Beam Ops.app" "$@"
EOF
    chmod +x "$LAUNCHER_DIR/beam-ops"
    echo "Installed beam-ops launcher to $LAUNCHER_DIR/beam-ops"
else
    curl -fsSL "$BASE_URL/$FILE" -o "$LAUNCHER_DIR/beam-ops"
    chmod +x "$LAUNCHER_DIR/beam-ops"
    echo "Installed beam-ops to $LAUNCHER_DIR/beam-ops"
fi

if ! echo "$PATH" | grep -q "$LAUNCHER_DIR"; then
    case "$OS" in
        Darwin) PROFILE="$REAL_HOME/.zshrc" ;;
        *)      PROFILE="$REAL_HOME/.bashrc" ;;
    esac
    if ! grep -q 'HOME/bin' "$PROFILE" 2>/dev/null; then
        echo "" >> "$PROFILE"
        echo 'export PATH="$HOME/bin:$PATH"' >> "$PROFILE"
        echo "Added \$HOME/bin to PATH in $PROFILE — run: source $PROFILE"
    fi
fi
