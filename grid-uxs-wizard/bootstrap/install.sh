#!/bin/sh
set -eu
umask 077

DEFAULT_FILE_SERVER=''
file_server=${GRID_UXS_FILE_SERVER:-$DEFAULT_FILE_SERVER}
role=
action=setup
install_only=no

fail() { printf 'grid-uxs-wizard: %s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'EOF'
Download the Grid Networked UxS wizard and start setup.

  install.sh setup|update [--role gcs|uxs] [--file-server URL] [--install-only]
                   [-- WIZARD_SETUP_FLAGS...]

Run as your normal user. Initial setup requests sudo for system installation.
--install-only installs the wizard without changing Beam or TVPN services.
EOF
}
case "${1-}" in setup|update) action=$1; shift ;; esac
while [ "$#" -gt 0 ]; do
    case "$1" in
        --role) [ "$#" -ge 2 ] || fail 'Provide --role gcs or uxs'; role=$2; shift 2 ;;
        --file-server) [ "$#" -ge 2 ] || fail 'Provide --file-server URL'; file_server=$2; shift 2 ;;
        --install-only) install_only=yes; shift ;;
        --help|-h) usage; exit 0 ;;
        --) shift; break ;;
        *) fail "Unknown bootstrap option: $1" ;;
    esac
done
[ "$(id -u)" != 0 ] || fail 'Run the downloader without sudo; setup will request elevation when needed'
[ -n "${HOME-}" ] && [ -d "$HOME" ] || fail 'A user home directory is required'
case "$file_server" in ''|*'@'*|*'?'*|*'#'*|*[[:space:]]*) fail 'Provide a file-server base URL with --file-server' ;; esac
file_server=${file_server%/}
case "$file_server" in
    https://?*) ;;
    http://*)
        host=${file_server#http://}; host=${host%%/*}; host=${host%%:*}
        printf '%s\n' "$host" | awk -F. '
            NF != 4 { exit 1 }
            { for (i=1;i<=4;i++) if ($i !~ /^[0-9]+$/ || $i>255) exit 1 }
            !($1==127 || $1==10 || ($1==192 && $2==168) || ($1==172 && $2>=16 && $2<=31)) { exit 1 }
        ' || fail 'HTTP downloads require a private or loopback IPv4 address'
        ;;
    *) fail 'Downloads require HTTPS, or HTTP on a local IP address' ;;
esac
for tool in curl tar awk mktemp uname; do command -v "$tool" >/dev/null 2>&1 || fail "Install $tool before continuing"; done
if command -v sha256sum >/dev/null 2>&1; then
    digest() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    digest() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    fail 'Install sha256sum or shasum before continuing'
fi
case "$(uname -s)" in Darwin) platform=darwin ;; Linux) platform=linux ;; *) fail 'This bootstrap supports macOS and Linux' ;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) fail 'Unsupported processor architecture' ;; esac
if [ -z "$role" ]; then
    ( : </dev/tty ) 2>/dev/null || fail 'Provide --role gcs or uxs when no terminal is available'
    while :; do
        printf '\nWhich computer are you setting up?\n\n  1. GCS\n  2. UxS mission computer\n\nChoose a number: ' >/dev/tty
        IFS= read -r answer </dev/tty || fail 'Setup cancelled'
        case "$answer" in 1|gcs) role=gcs; break ;; 2|uxs) role=uxs; break ;; esac
    done
fi
case "$role" in gcs) ;; uxs) [ "$platform" = linux ] || fail 'The UxS mission computer must run Linux' ;; *) fail 'Choose role gcs or uxs' ;; esac
wizard_name=grid-uxs-wizard
archive_name=$role-$platform-$arch.tar.gz
cache=${XDG_CACHE_HOME:-$HOME/.cache}/grid-uxs-wizard
releases=${XDG_DATA_HOME:-$HOME/.local/share}/grid-uxs-wizard/releases
bin_dir=$HOME/.local/bin
mkdir -p "$cache" "$releases" "$bin_dir"
lock=$cache/bootstrap.lock
mkdir "$lock" 2>/dev/null || fail "Another download is active; if interrupted, remove $lock and retry"
stage=
checksum_file=
cleanup() {
    [ -z "$stage" ] || rm -rf "$stage"
    [ -z "$checksum_file" ] || rm -f "$checksum_file"
    rmdir "$lock" 2>/dev/null || :
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
checksum_file=$(mktemp "$cache/checksum.XXXXXX")
curl -fsSL --noproxy '*' --proto '=https,http' --proto-redir '=https' --connect-timeout 10 \
    "$file_server/$archive_name.sha256" -o "$checksum_file"
expected=$(awk 'NR==1 {print $1}' "$checksum_file")
case "$expected" in ''|*[!0-9a-f]*) fail 'Invalid release checksum' ;; esac
[ "${#expected}" = 64 ] || fail 'Invalid release checksum'
archive=$cache/$role-$platform-$arch-$expected.tar.gz
if [ ! -f "$archive" ] || [ "$(digest "$archive")" != "$expected" ]; then
    printf '  Downloading the %s wizard and installation files...\n' "$role" >&2
    if curl -fL --noproxy '*' --proto '=https,http' --proto-redir '=https' --connect-timeout 10 \
        --retry 3 --retry-delay 1 --continue-at - --progress-bar "$file_server/$archive_name" -o "$archive"; then :
    else fail 'Download interrupted; run the same command to resume'; fi
fi
if [ "$(digest "$archive")" != "$expected" ]; then
    mv "$archive" "$archive.rejected-$(date +%s)"
    fail 'Release checksum mismatch; retry to download a fresh copy'
fi
version=$releases/$role-$platform-$arch-$expected
if [ ! -f "$version/.grid-uxs-wizard-install" ]; then
    tar -tzf "$archive" | awk -v wizard="$wizard_name" '
        { if (seen[$0]++) exit 1 }
        $0 != wizard && $0 !~ /^(grid-uxs-wizard.json|THIRD_PARTY_NOTICES.txt|bundle\/?|bundle\/(beam.tar.gz|tvpn.tar.gz|install.json))$/ { exit 1 }
    ' || fail 'Unexpected paths in the installation download'
    tar -tvzf "$archive" | awk 'substr($0,1,1) !~ /[-d]/ {exit 1}' || fail 'Links are not allowed in the installation download'
    stage=$(mktemp -d "$releases/.download.XXXXXX")
    tar -xzf "$archive" -C "$stage" --no-same-owner
    for file in "$wizard_name" grid-uxs-wizard.json bundle/install.json bundle/beam.tar.gz bundle/tvpn.tar.gz; do
        [ -f "$stage/$file" ] && [ ! -L "$stage/$file" ] || fail "Installation file missing: $file"
    done
    chmod 755 "$stage/$wizard_name"
    printf '%s\n' "$expected" > "$stage/.grid-uxs-wizard-install"
    [ ! -e "$version" ] || fail "Incomplete installation at $version; remove it and retry"
    mv "$stage" "$version"; stage=
fi
launcher=$bin_dir/$wizard_name
if [ -e "$launcher" ] || [ -L "$launcher" ]; then
    [ -L "$launcher" ] || fail "Existing command at $launcher was preserved; move it before retrying"
    target=$(readlink "$launcher")
    case "$target" in "$releases/"*/"$wizard_name") ;; *) fail "Unmanaged command at $launcher was preserved" ;; esac
fi
ln -s "$version/$wizard_name" "$lock/launcher"
mv -f "$lock/launcher" "$launcher"
path_line='export PATH="$HOME/.local/bin:$PATH"'
case "${SHELL-}" in
    */zsh) profiles="$HOME/.zprofile
$HOME/.zshrc" ;;
    */bash) profiles="$HOME/.profile
$HOME/.bashrc"; [ ! -f "$HOME/.bash_profile" ] || profiles="$profiles
$HOME/.bash_profile" ;;
    */sh|'') profiles="$HOME/.profile" ;;
    *) profiles= ;;
esac
old_ifs=$IFS
IFS='
'
for profile in $profiles; do
    if [ ! -f "$profile" ] || ! grep -Fqx "$path_line" "$profile"; then
        printf '\n%s\n' "$path_line" >> "$profile"
    fi
done
IFS=$old_ifs
printf '\nWizard installed: %s\n' "$launcher" >&2
case ":${PATH-}:" in *":$bin_dir:"*) ;; *) printf 'For this terminal, run:\n\n  %s\n\n' "$path_line" >&2 ;; esac
if [ "$install_only" = yes ]; then
    printf 'Start setup in a new terminal:\n\n  grid-uxs-wizard setup --role %s\n\n' "$role" >&2
    exit 0
fi
if [ "$action" = update ]; then
    ( : </dev/tty ) 2>/dev/null || fail 'Wizard installed; run its update command from an interactive terminal'
    cleanup
    trap - EXIT INT TERM HUP
    exec "$launcher" update "$@" </dev/tty
fi
command -v sudo >/dev/null 2>&1 || fail 'Install sudo before starting system setup'
( : </dev/tty ) 2>/dev/null || fail 'Wizard installed; start setup from an interactive terminal'
printf '  Starting setup; sudo is required for system installation.\n' >&2
cleanup
trap - EXIT INT TERM HUP
exec "$launcher" setup --role "$role" "$@" </dev/tty
