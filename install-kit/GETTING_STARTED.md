---
title: "Install Beam and connect a Node"
description: "Install the Beam bundle, authenticate, update Node firmware, and register a USB connection."
audience: "For developers and integrators setting up Beam on Linux."
lastUpdated: 2026-10-02
---

Install Beam, authorize access to your workspace, update the Node, then connect
it over USB. Run each command separately and check its result before continuing.

## What you'll need

- A Beam bundle ZIP and its SHA-256 checksum supplied by your administrator.
- Linux ARM64 with a user systemd session. This bundle includes Java 21 and
  `newtmgr` for firmware updates. Other macOS/Linux architectures require Java
  21 and a compatible `newtmgr` installation. These steps use Linux commands.
- Internet access and an organization administrator to approve sign-in.
- One Node, its hardware flavor, and a USB data cable. Your Linux user needs
  permission to access its serial port.

Replace `BUNDLE.zip`, `BUNDLE_DIRECTORY`, `WORKSPACE_ID`, `NODE_SERIAL`, and
`USB_PORT`, and the radio-profile placeholders below with the values for your installation. The firmware examples are
for PC02 hardware and Bond 3.30.0-rc; use the files matching your Node.

## Verify and install the bundle

1. Download the supplied ZIP. If installing on a remote computer, transfer the
   ZIP with your normal secure file-transfer tool.
2. Verify its checksum against the value supplied with the bundle:

   ```sh
   sha256sum BUNDLE.zip
   ```

   Stop if the checksum differs. Download a fresh copy before proceeding.
3. Extract the bundle:

   ```sh
   unzip BUNDLE.zip
   ```

4. Enter its extracted directory:

   ```sh
   cd BUNDLE_DIRECTORY
   ```

5. Install Beam and the bundled dependencies:

   ```sh
   sh install.sh
   ```

   Each checksum should report `OK`. The installer writes Beam to `~/bin` and
   does not require sudo. It does not flash the Node.
6. Add the installation directory to this terminal's command path:

   ```sh
   export PATH="$HOME/bin:$PATH"
   ```

7. Check that Beam starts:

   ```sh
   beam --help
   ```

## Sign in and choose a workspace

1. Start Beam:

   ```sh
   beam up
   ```

2. Start sign-in:

   ```sh
   beam auth sign-in
   ```

   Enter your organization's API domain when prompted. Open the printed link
   in a browser and sign in as an organization administrator. Wait for
   `Signed in.` before continuing. Keep the approval link private.
3. List available workspaces:

   ```sh
   beam workspace list
   ```

4. Activate the intended workspace using its ID from that list:

   ```sh
   beam workspace activate --id WORKSPACE_ID
   ```

## Check and update the Node

1. Connect one Node over USB and power it on manually. Wait for USB discovery.
2. Check its serial and current firmware:

   ```sh
   beam device info
   ```

   Confirm the serial matches your intended Node. Choose firmware using its
   hardware flavor; do not use a PC02 image for another hardware flavor.
3. Stop Beam before firmware updates:

   ```sh
   beam down
   ```

4. Extract the matching firmware ZIP. For PC02:

   ```sh
   unzip firmware/3.30.0-rc/pc02-3.30.0-rc.zip -d firmware-selected
   ```

5. Update the application firmware:

   ```sh
   beam device update-firmware --firmware firmware-selected/pc02/pc02-3.30.0-app_update.bin
   ```

   Keep the USB cable connected. Continue only after the update reports success.
   Beam handles bootloader entry and uses the installed `newtmgr` executable.
6. Update the network firmware:

   ```sh
   beam device update-firmware --network-firmware firmware-selected/pc02/pc02-3.30.0-net_core_app_update.bin
   ```

   Continue only after the update reports success. The network core has its
   own version; it need not match the application version.

## Switch the Node to USB

1. If the Node already reports `USB Locked: true` and `Connection Mode: USB`,
   proceed to registration. Otherwise, keep Beam stopped and apply USB lock:

   ```sh
   beam device apply-usb-lock --serial NODE_SERIAL
   ```

   The lock prevents changes back to Bluetooth. Changing connection mode
   triggers a reboot. The Node may remain off afterward; power it on manually.
2. Start Beam:

   ```sh
   beam up
   ```

3. Wait for USB discovery, then inspect the Node:

   ```sh
   beam device info
   ```

   Confirm the intended serial, `USB Locked: true`, and `Connection Mode: USB`.
   Device info can be available while the Node is off, so confirm it is powered
   on before registration.

## Register and verify the connection

1. Close any existing Beam device connection:

   ```sh
   beam device disconnect
   ```

   `No device is connected` is acceptable at this step.
2. Connect and register Beam's active identity:

   ```sh
   beam device connect --usb USB_PORT --register
   ```

   Use the serial-port path assigned by your host. This command works with the
   authenticated integration identity and does not require a contact lookup.
3. Wait a few seconds, then inspect the registration:

   ```sh
   beam device info
   ```

   Confirm the intended serial, USB mode, USB lock, expected app firmware, and
   a nonzero `Account Id`. Check the `Assigned To` name when available.
4. Check the service:

   ```sh
   beam status
   ```

   Expect `Beam service (user): Up`. On Linux, `User lingering: Enabled` means
   the service continues after logout.

## Apply the workspace radio settings

Obtain the full workspace radio profile from your workspace administrator.
Registration does not confirm that the Node's existing radio profile matches
it. Keep Beam running and the intended Node powered on and connected over USB.
The API below writes through the daemon's existing device connection.

1. Create `radio-settings.json` with the following template. Replace both
   frequency placeholders with integer values in Hz, and replace the quoted
   enum placeholders with the administrator-supplied values before sending it:

   ```json
   {
     "settings": {
       "lowSpeedFrequencyHz": LOW_FREQUENCY_HZ,
       "highSpeedFrequencyHz": HIGH_FREQUENCY_HZ,
       "lowSpeedSpreadFactor": "LOW_SPREAD_FACTOR",
       "highSpeedSpreadFactor": "HIGH_SPREAD_FACTOR",
       "lowSpeedBandwidth": "LOW_BANDWIDTH",
       "highSpeedBandwidth": "HIGH_BANDWIDTH",
       "radioRegion": "RADIO_REGION",
       "radioPowerMode": "RADIO_POWER_MODE",
       "radioMode": "RadioModeEnabled"
     }
   }
   ```

   This template becomes valid JSON after substitution. Enum spelling matters:
   SF5 is `RadioSpreadFactor05`, 500 kHz is `RadioBandwidth500KHz`, and high
   power is `PowerModeHigh`. These explain the format; use the actual workspace
   profile rather than treating them as defaults. Keep only the intended Node
   attached: this endpoint operates on the daemon-connected device.
2. Apply the profile through the local Beam API:

   ```sh
   curl --fail-with-body --silent --show-error --max-time 60 --header 'Content-Type: application/json' --data-binary @radio-settings.json http://localhost:9091/api/device/daemon-settings
   ```

   Check the returned `results` entry for `success: true`, the intended
   `deviceSerial`, and settings matching the requested profile. An HTTP success
   alone is insufficient; `success: false` means the write was not confirmed.
   This endpoint also saves settings through Beam's workspace-settings source.
3. Read the device settings back:

   ```sh
   curl --fail-with-body --silent --show-error http://localhost:9091/api/device/daemon-settings
   ```

   Confirm both frequencies, both spreading factors, both bandwidths, region,
   power, and enabled mode match the supplied profile. Require `success: true`
   and the intended serial. Do not infer the workspace profile from the Node's
   previous channel or from a named-channel display alone.

## Troubleshooting

- **No devices found:** check the cable, serial permissions, and physical power.
  Wait for USB enumeration, then repeat `beam device info`.
- **Device is already connected:** disconnect before repeating
  `connect --register`; that message means registration did not run.
- **Account Id remains 0:** power the Node on manually, then disconnect and
  reconnect with `--register`. Off-state firmware ignores registration.
- **Beam daemon is running:** run `beam down` before DFU or USB-lock commands.
- **Peer client is down immediately after startup:** allow startup to finish,
  then check `beam status` again. If it persists, inspect `beam log --help`.
- **Registration prints both failure and success:** rely on device readback.
  This preview's contact-registration command can print a misleading success
  line. Use `connect --register` for the active integration identity.

## Related

- [Installer](install.sh) — the installer included in this bundle.
- `beam device --help` — device and firmware commands.
- `beam log --help` — inspect diagnostic logs.
