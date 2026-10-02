#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: bash wizard.sh (--url URL | --bundle-file PATH) --sha256 HEX --serial NODE_SERIAL --hardware pc02 [--workspace-id ID] [--work-dir DIR] [--download-only]

Download and verify a Beam bundle, then guide installation and optional Node DFU.
The URL may be a TVPN HTTP endpoint or a future GCP object URL. Use --bundle-file
after copying a large bundle with scp when HTTP transfer is unreliable.
EOF
}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
confirm() {
    local reply
    read -r -p "$1 [y/N] " reply
    [[ "$reply" == [yY] || "$reply" == [yY][eE][sS] ]]
}

url=""
bundle_file=""
expected_sha=""
serial=""
hardware=""
workspace_id=""
work_dir="${HOME}/beam-bundle-wizard"
download_only=0
while (( $# )); do
    case "$1" in
        --url|--bundle-file|--sha256|--serial|--hardware|--workspace-id|--work-dir)
            (( $# >= 2 )) || die "$1 needs a value"
            case "$1" in
                --url) url=$2 ;;
                --bundle-file) bundle_file=$2 ;;
                --sha256) expected_sha=$2 ;;
                --serial) serial=$2 ;;
                --hardware) hardware=$2 ;;
                --workspace-id) workspace_id=$2 ;;
                --work-dir) work_dir=$2 ;;
            esac
            shift 2 ;;
        --download-only) download_only=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "Unknown option: $1" ;;
    esac
done

if [[ -n "$url" && -n "$bundle_file" ]]; then
    die "Use either --url or --bundle-file, not both"
fi
if [[ -n "$url" ]]; then
    [[ "$url" == http://* || "$url" == https://* ]] || die "Use an HTTP or HTTPS bundle URL"
elif [[ -n "$bundle_file" ]]; then
    [[ -f "$bundle_file" ]] || die "Bundle file does not exist: $bundle_file"
else
    die "Provide --url or --bundle-file"
fi
[[ "$expected_sha" =~ ^[[:xdigit:]]{64}$ ]] || die "--sha256 must be a 64-character SHA-256 digest"
[[ -n "$serial" || "$download_only" -eq 1 ]] || die "--serial is required for installation"
for command in cmp curl python3 readlink unzip sha256sum; do
    command -v "$command" >/dev/null || die "Required command missing: $command"
done

mkdir -p "$work_dir"
work_dir=$(cd "$work_dir" && pwd)
archive="$work_dir/beam-bundle.zip"
if [[ ! -f "$archive" ]] || [[ "$(sha256sum "$archive" | cut -d ' ' -f 1)" != "${expected_sha,,}" ]]; then
    rm -f "$archive"
    if [[ -n "$bundle_file" ]]; then
        printf 'Copying verified source file %s\n' "$bundle_file"
        cp "$bundle_file" "$archive"
    else
        printf 'Downloading %s\n' "$url"
        curl --fail --location --show-error --retry 2 --connect-timeout 10 --max-time 3600 \
            --continue-at - --output "$archive.part" "$url"
        mv "$archive.part" "$archive"
    fi
fi
actual_sha=$(sha256sum "$archive" | cut -d ' ' -f 1)
[[ "$actual_sha" == "${expected_sha,,}" ]] || die "Bundle SHA-256 mismatch; expected $expected_sha, got $actual_sha"

bundle_name=$(unzip -Z -1 "$archive" | head -n 1 | cut -d/ -f1)
[[ "$bundle_name" == beam-* && "$bundle_name" != *..* ]] || die "Unexpected bundle root in ZIP"
extract_dir="$work_dir/extracted"
mkdir -p "$extract_dir"
unzip -oq "$archive" -d "$extract_dir"
bundle_dir="$extract_dir/$bundle_name"
[[ -f "$bundle_dir/SHA256SUMS" && -f "$bundle_dir/install.sh" ]] || die "Incomplete Beam bundle"
(cd "$bundle_dir" && sha256sum -c SHA256SUMS)
printf '\nVerified bundle: %s\n' "$bundle_dir"
sed -n '1,13p' "$bundle_dir/MANIFEST.txt"
if (( download_only )); then
    printf '\nDownload-only check complete; Beam and the Node were not changed.\n'
    exit 0
fi

beam_bin="${HOME}/bin/beam"
case "$hardware" in pc01|pc02|pc04) ;; *) die "--hardware must be pc01, pc02, or pc04" ;; esac
firmware_releases=("$bundle_dir/firmware/"*)
(( ${#firmware_releases[@]} == 1 )) || die "Expected one firmware release directory"
firmware_release=$(basename "${firmware_releases[0]}")
firmware_zip="${firmware_releases[0]}/${hardware}-${firmware_release}.zip"
[[ -f "$firmware_zip" ]] || die "No firmware ZIP for $hardware"
printf '\nNode %s: selected hardware %s\n' "$serial" "$hardware"
if [[ -x "$beam_bin" ]]; then printf 'Existing Beam launcher: %s\n' "$beam_bin"; fi
printf 'Sequence: install Beam, authenticate, choose workspace, DFU, apply USB lock, power on and register Node, beam up.\n'
confirm 'Install this Beam bundle?' || { printf 'No changes made.\n'; exit 0; }

backup_dir="$work_dir/backup-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$backup_dir"
for file in beam beam.jar beam-java-path; do
    if [[ -f "$HOME/bin/$file" ]]; then
        cp -p "$HOME/bin/$file" "$backup_dir/$file"
    fi
done
printf 'Previous Beam files saved in %s\n' "$backup_dir"
install_options=()
if [[ -x "$beam_bin" ]]; then
    "$beam_bin" down
    install_options+=(--force)
fi
sh "$bundle_dir/install.sh" "${install_options[@]}"
cmp -s "$HOME/bin/beam.jar" "$bundle_dir/somewear-beam.jar" || die "Installed Beam JAR does not match the verified bundle"
"$beam_bin" --help >/dev/null
printf 'Installed Beam JAR SHA-256: %s\n' "$(sha256sum "$HOME/bin/beam.jar" | cut -d ' ' -f 1)"
printf 'Starting Beam temporarily for browser or approval-link sign-in.\n'
"$beam_bin" up
"$beam_bin" auth sign-in
"$beam_bin" workspace list
if [[ -z "$workspace_id" ]]; then
    read -r -p 'Workspace ID to activate: ' workspace_id
fi
[[ "$workspace_id" =~ ^[0-9]+$ ]] || die "Enter a numeric workspace ID"
"$beam_bin" workspace activate --id "$workspace_id"
if ! "$beam_bin" device info --json > "$work_dir/device-before-dfu.json"; then
    "$beam_bin" down
    die "Could not read attached Node information. Beam remains stopped."
fi
if ! WIZARD_SERIAL="$serial" WIZARD_HARDWARE="$hardware" python3 - "$work_dir/device-before-dfu.json" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding='utf-8') as source:
    devices = json.load(source)
expected_flavor = 'HardwareFlavor' + os.environ['WIZARD_HARDWARE'].upper()
if len(devices) != 1:
    raise SystemExit(f'Expected one connected Node before DFU; found {len(devices)}')
device = devices[0]
if device.get('serial') != os.environ['WIZARD_SERIAL']:
    raise SystemExit(f"Connected Node serial does not match: {device.get('serial')}")
if device.get('hardwareFlavor') != expected_flavor:
    raise SystemExit(f"Connected Node hardware does not match: {device.get('hardwareFlavor')}")
print(f"Verified Node {device['serial']} hardware {device['hardwareFlavor']}")
PY
then
    "$beam_bin" down
    die "Node preflight failed. Beam remains stopped."
fi
confirm "Flash Bond 3.30.0-rc on the single attached $hardware Node $serial?" || {
    "$beam_bin" down
    die "DFU cancelled. Beam remains stopped."
}
shopt -s nullglob
usb_nodes=(/dev/serial/by-id/usb-Somewear*)
if (( ${#usb_nodes[@]} != 1 )) || [[ "${usb_nodes[0]}" != *"$serial"* ]]; then
    "$beam_bin" down
    die "DFU requires exactly one attached Somewear USB device matching $serial. Beam remains stopped."
fi
usb_device=$(readlink -f "${usb_nodes[0]}")
if [[ ! -e "$usb_device" ]]; then
    "$beam_bin" down
    die "Attached USB device disappeared. Beam remains stopped."
fi
firmware_dir="$work_dir/selected-firmware"
mkdir -p "$firmware_dir"
if ! unzip -oq "$firmware_zip" -d "$firmware_dir"; then
    "$beam_bin" down
    die "Could not extract firmware. Beam remains stopped."
fi
app_images=("$firmware_dir/$hardware/"*-app_update.bin)
network_images=("$firmware_dir/$hardware/"*-net_core_app_update.bin)
if (( ${#app_images[@]} != 1 || ${#network_images[@]} != 1 )); then
    "$beam_bin" down
    die "Expected one app and one network update binary. Beam remains stopped."
fi
printf 'App image: %s\nNetwork image: %s\n' "${app_images[0]}" "${network_images[0]}"
if ! "$beam_bin" device enter-bootloader; then
    "$beam_bin" down
    die "Could not enter bootloader mode. Beam remains stopped."
fi
"$beam_bin" down
for attempt in {1..20}; do
    [[ -e "$usb_device" ]] && break
    sleep 1
done
[[ -e "$usb_device" ]] || die "Bootloader USB port $usb_device did not reappear. Beam remains stopped."
"$beam_bin" device update-firmware --device "$usb_device" --firmware "${app_images[0]}" > "$work_dir/app-dfu.log" 2>&1 || {
    cat "$work_dir/app-dfu.log"; die "Application DFU failed. Beam remains stopped."
}
cat "$work_dir/app-dfu.log"
grep -Fq 'Success - Firmware update complete.' "$work_dir/app-dfu.log" || die "Application DFU did not report success. Beam remains stopped."

confirm 'Continue with network firmware DFU?' || die "Network DFU cancelled. Beam remains stopped."
for attempt in {1..20}; do
    [[ -e "$usb_device" ]] && break
    sleep 1
done
[[ -e "$usb_device" ]] || die "Attached USB device disappeared after app DFU. Beam remains stopped."
"$beam_bin" device update-firmware --device "$usb_device" --network-firmware "${network_images[0]}" > "$work_dir/network-dfu.log" 2>&1 || {
    cat "$work_dir/network-dfu.log"; die "Network DFU failed. Beam remains stopped."
}
cat "$work_dir/network-dfu.log"
grep -Fq 'Success - Firmware update complete.' "$work_dir/network-dfu.log" || die "Network DFU did not report success. Beam remains stopped."

confirm "Apply USB lock to Node $serial?" || die "USB lock cancelled. Beam remains stopped."
"$beam_bin" device apply-usb-lock --serial "$serial"
"$beam_bin" up
printf 'Waiting for the Beam service to discover the Node.\n'
sleep 5
confirm "Is Node $serial physically powered on?" || die "Power on the Node, then run: $beam_bin device disconnect; $beam_bin device connect --usb $usb_device --register"
"$beam_bin" device disconnect >/dev/null 2>&1 || true
"$beam_bin" device connect --usb "$usb_device" --register
sleep 5
printf '\nProvisioning check:\n'
"$beam_bin" device info
printf '\nBeam started. Confirm the serial matches %s and Account Id is no longer 0.\n' "$serial"
