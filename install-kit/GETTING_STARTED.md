# Beam getting started

This bundle contains a local preview build of Beam, its installer, launcher, and
Bond 3.30.0-rc firmware for pc01, pc02, and pc04 hardware.
Source revisions and build details are in `MANIFEST.txt`. The bundle includes
Java 21 and `newtmgr` for Linux ARM64, plus Windows x64 `newtmgr.exe` for manual
firmware work. The Beam installer does not support Windows.
The matching `*-wizard.sh` is also distributed beside the ZIP. It downloads
the ZIP from a supplied URL, checks its SHA-256 and internal checksums, then
guides installation and optional device firmware updates.

## Requirements

- Linux ARM64 with bundled Java 21, or macOS/Linux x64 with Java 21 installed.
- On Linux, a working user systemd session for `beam up`.
- Internet access and a Somewear account or API key for your workspace.
- For radio connectivity, a Somewear Node and a USB data cable. Your user must
  have permission to access the serial device on Linux.
- Power the Node on before registration. A Node in deep sleep accepts device
  discovery but ignores registration commands.

## Install

Extract the ZIP, open a terminal, and enter the extracted directory. Linux ARM64
uses the bundled runtime. On other supported hosts, check Java:

```sh
java -version
```

If the default Java is older than 21, set `JAVA_HOME` to your Java 21 installation
directory. The installer saves the selected Java executable for the launcher.

```sh
sh install.sh
export PATH="$HOME/bin:$PATH"
beam --help
```

Installation verifies the included checksums and copies `beam`, `beam.jar`, and
`beam-java-path` into `~/bin`. On Linux ARM64 it also installs `newtmgr` and
`beam-runtime`. It does not need sudo. Add the PATH line to your
shell startup file to keep it for future terminals. Use `command -v beam` to
confirm you are running this installation.

The firmware remains in this extracted bundle under `firmware/3.30.0-rc/`.
Keep the bundle until the device update is complete. Each pcXX ZIP contains
application and network update binaries, factory images, and debug files.
Choose the ZIP matching the device's hardware flavor; do not infer the flavor
from the ZIP's version number. The included `build-manifest.json` describes
pc04 only. The installer does not flash a device.

To choose another directory, use `sh install.sh --bin-dir /absolute/path/bin`
and put that directory on PATH. Use a path without spaces for service startup.

## Start and sign in

Connect your Node if using radio connectivity, then run:

```sh
beam up
beam status
```

`beam up` creates/starts the platform's background service and prompts for sign-in
when needed. If authentication is still required, run:

```sh
beam auth sign-in
```

Follow the browser or approval-link flow. On a headless host, open the printed
link from an administrator workstation to authorize the sign-in.
Explore workspace and device commands with:

```sh
beam workspace --help
beam device --help
```

Stop the service with `beam down`. Inspect diagnostic commands with `beam log --help`.
This bundle's installer has been checked in isolation; starting the service and
connecting real hardware still need validation on the recipient's host.

## Register the Node

List workspaces before activation so that you select the intended workspace ID:

```sh
beam workspace list
beam workspace activate --id WORKSPACE_ID
```

Use the browser or approval-link sign-in flow before this step. For an
integration identity, register its active Beam identity during a fresh USB
connection:

```sh
beam device disconnect
beam device connect --usb /dev/ttyACM0 --register
sleep 5
beam device info
```

`beam device register --user-id ...` is for a workspace contact or resource; it
does not register an integration identity that is absent from the contact list.
If `Account Id` remains `0`, make sure the Node is physically powered on, then
disconnect and reconnect with `--register`. After a successful registration,
the displayed account value can be an on-device identifier that Beam does not
resolve to a contact name.

## Firmware update preparation

Check the device's reported hardware flavor and current firmware before choosing
pc01, pc02, or pc04. Extract that hardware's ZIP to access its
`*-app_update.bin` and `*-net_core_app_update.bin` files. Beam's
`device update-firmware` command takes a binary path, not the release ZIP, and
requires the Beam daemon to be stopped. Confirm the device is in the required
bootloader state before flashing, then verify the reported versions after it
reconnects. No hardware DFU has been validated with this preview bundle yet.

## Download wizard

Get the wizard from the same delivery location as the ZIP, then run it with the
bundle URL, the SHA-256 supplied by the bundle creator, and the intended Node
serial. For example:

```sh
bash beam-bundle-wizard.sh --url 'http://TVPN_HOST:PORT/beam-BUILD.zip' \
  --sha256 'EXPECTED_64_CHARACTER_SHA256' --serial 'NODE_SERIAL' \
  --hardware pc02
```

Use `--download-only` to verify the transfer without stopping Beam or changing
the Node. Select hardware from a prior device record. The interactive path
installs Beam, starts it temporarily for browser or approval-link sign-in, lists
available workspaces, prompts for the workspace ID, then stops it for
application and network DFU, applies USB lock, starts Beam, and registers a
powered-on Node. It asks before each device-changing step.
If a DFU step fails or is cancelled after Beam stops, Beam remains stopped for
inspection. Keep the extracted directory for the firmware and DFU logs.

For large TVPN transfers, copy the ZIP with `scp`, verify it with the supplied
SHA-256, and run the wizard with `--bundle-file /path/to/beam-bundle.zip`
instead of `--url`. URL downloads resume into a `.part` file when the server
supports HTTP ranges.

## Replace an existing build

Stop the existing Beam using its current launcher, then install this bundle:

```sh
beam down
sh install.sh --force
beam --help
beam up
```

The installer replaces files but does not migrate configuration or automatically
stop/restart services. Keep your previous bundle if you need to roll back; stop
Beam and reinstall that bundle. Avoid `beam upgrade` to retain this preview build.

To remove this installation, run `beam down`, then remove the installed Beam
files and, on Linux ARM64, `newtmgr` and `beam-runtime` from your chosen
directory. Your Beam configuration remains on disk.
