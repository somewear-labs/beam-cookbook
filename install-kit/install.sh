#!/bin/sh
set -eu
BUNDLE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN_DIR="$HOME/bin"
FORCE=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --bin-dir) [ "$#" -ge 2 ] || { echo "--bin-dir needs a directory" >&2; exit 2; }; BIN_DIR=$2; shift 2 ;;
        --force) FORCE=1; shift ;;
        -h|--help) echo "Usage: sh install.sh [--bin-dir DIRECTORY] [--force]"; exit 0 ;;
        *) echo "Unknown option: $1" >&2; exit 2 ;;
    esac
done
case "$(uname -s)" in Darwin|Linux) ;; *) echo "This installer supports macOS and Linux." >&2; exit 1 ;; esac
ARCH=$(uname -m)
if [ "$(uname -s)" = Linux ] && [ "$ARCH" = aarch64 ]; then
    [ -f "$BUNDLE_DIR/dependencies/java21-linux-arm64.tar.gz" ] || { echo "Bundled Java runtime missing." >&2; exit 1; }
    [ -f "$BUNDLE_DIR/dependencies/newtmgr-linux-arm64" ] || { echo "Bundled newtmgr missing." >&2; exit 1; }
    JAVA_BIN="$BIN_DIR/beam-runtime/bin/java"
elif [ -n "${JAVA_HOME:-}" ]; then
    JAVA_BIN="$JAVA_HOME/bin/java"
else
    JAVA_BIN=$(command -v java || true)
fi
(
    cd "$BUNDLE_DIR"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -c SHA256SUMS
    else
        sha256sum -c SHA256SUMS
    fi
)
for name in beam beam.jar beam-java-path newtmgr beam-runtime; do
    if { [ -e "$BIN_DIR/$name" ] || [ -L "$BIN_DIR/$name" ]; } && [ "$FORCE" -ne 1 ]; then
        echo "$BIN_DIR/$name already exists. Stop existing Beam, then use --force to replace it." >&2
        exit 1
    fi
    [ ! -L "$BIN_DIR/$name" ] || { echo "Refusing symlink: $BIN_DIR/$name" >&2; exit 1; }
    if [ "$name" = beam-runtime ]; then
        if [ -e "$BIN_DIR/$name" ]; then
            [ "$FORCE" -eq 1 ] && [ -d "$BIN_DIR/$name" ] && [ -f "$BIN_DIR/$name/release" ] || {
                echo "Refusing existing runtime directory: $BIN_DIR/$name" >&2; exit 1;
            }
        fi
    else
        [ ! -d "$BIN_DIR/$name" ] || { echo "Refusing directory: $BIN_DIR/$name" >&2; exit 1; }
    fi
done
mkdir -p "$BIN_DIR"
if [ "$(uname -s)" = Linux ] && [ "$ARCH" = aarch64 ]; then
    RUNTIME_STAGE=$(mktemp -d "$BIN_DIR/.beam-runtime.XXXXXX")
    trap 'rm -rf "$RUNTIME_STAGE"' EXIT HUP INT TERM
    tar -xzf "$BUNDLE_DIR/dependencies/java21-linux-arm64.tar.gz" -C "$RUNTIME_STAGE"
    [ -x "$RUNTIME_STAGE/jre/bin/java" ] || { echo "Bundled Java runtime is incomplete." >&2; exit 1; }
    JAVA_VERSION=$("$RUNTIME_STAGE/jre/bin/java" -version 2>&1)
    if [ -d "$BIN_DIR/beam-runtime" ]; then
        mv "$BIN_DIR/beam-runtime" "$BIN_DIR/beam-runtime-backup-$(date -u +%Y%m%dT%H%M%SZ)"
    fi
    mv "$RUNTIME_STAGE/jre" "$BIN_DIR/beam-runtime"
    cp "$BUNDLE_DIR/dependencies/newtmgr-linux-arm64" "$BIN_DIR/newtmgr"
    chmod 755 "$BIN_DIR/newtmgr"
else
    [ -n "$JAVA_BIN" ] && [ -x "$JAVA_BIN" ] || { echo "Install Java 21 and set JAVA_HOME before installing Beam." >&2; exit 1; }
    JAVA_VERSION=$("$JAVA_BIN" -version 2>&1)
    JAVA_BIN=$(CDPATH= cd -- "$(dirname -- "$JAVA_BIN")" && pwd)/java
fi
JAVA_MAJOR=$(printf '%s\n' "$JAVA_VERSION" | sed -n 's/.*version "\([0-9][0-9]*\).*/\1/p' | head -n 1)
case "$JAVA_MAJOR" in ''|*[!0-9]*) echo "Cannot determine Java version." >&2; exit 1 ;; esac
[ "$JAVA_MAJOR" -ge 21 ] || { echo "Java 21 or newer required; found Java $JAVA_MAJOR." >&2; exit 1; }
cp "$BUNDLE_DIR/somewear-beam.jar" "$BIN_DIR/beam.jar"
cp "$BUNDLE_DIR/beam" "$BIN_DIR/beam"
printf '%s\n' "$JAVA_BIN" > "$BIN_DIR/beam-java-path"
chmod 755 "$BIN_DIR/beam"
echo "Installed Beam in $BIN_DIR"
echo "Add that directory to PATH, then run: beam --help"
echo "See GETTING_STARTED.md to start and sign in."
