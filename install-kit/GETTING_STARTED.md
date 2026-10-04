---
title: "Install Beam and connect a Node"
description: "Install Beam, configure Grid host defaults, and provision a Node in your workspace."
audience: "For developers and integrators setting up Beam on Linux."
lastUpdated: 2026-10-03
---

Install Beam, configure Grid defaults, and provision your Node. Run each command
separately and check the result.

## What you'll need

- The release bundle and its SHA-256 checksum.
- Linux ARM64 with a user systemd session and serial-port permissions. Java 21
  and `newtmgr` are bundled; other architectures need compatible installations.
- Internet access and an organization admin to approve sign-in.
- One Node and a USB data cable.

Replace uppercase placeholders with your installation values. Firmware examples
use PC02 and Bond 3.30.0-rc; use the release matching your hardware.

## Verify and install the bundle

1. Download or securely copy the supplied ZIP to the host.
2. Verify its checksum against the value supplied with the bundle:

   ```sh
   sha256sum BUNDLE.zip
   ```

   Continue only if the checksum matches.
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

   Expect checksum results of `OK`. Installation writes to `~/bin`; no sudo needed.
6. Add the installation directory to this terminal's command path:

   ```sh
   export PATH="$HOME/bin:$PATH"
   ```

7. Check that Beam starts:

   ```sh
   beam --help
   ```

## Configure Beam for Grid

1. Apply the Grid host preset:

   ```sh
   beam config reset-defaults --type grid
   ```

   Review the changes and confirm. This enables automatic connection,
   connection maintenance, and automatic registration. Credentials and other
   properties are preserved. Use `--dry-run` to preview.

   If Beam is already running, add `--restart-beam` to apply the saved settings.

## Sign in and choose a workspace

1. Start Beam:

   ```sh
   beam up
   ```

2. Start sign-in:

   ```sh
   beam auth sign-in
   ```

   Accept the default domain or enter your organization's domain. Open the link
   in a browser, approve as an organization admin, and wait for `Signed in.`
3. List available workspaces:

   ```sh
   beam workspace list
   ```

4. Activate the intended workspace using its ID from that list:

   ```sh
   beam workspace activate --id WORKSPACE_ID
   ```

## Check the Node firmware

1. Connect one Node over USB and power it on manually. Wait for USB discovery.
2. Check its serial and current firmware:

   ```sh
   beam device info
   ```

   Check the serial and firmware. If the supplied release is already installed,
   skip to USB setup. Otherwise, use images matching the Node's hardware.
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

   Keep USB connected. Wait for `Success - Firmware update complete.`
6. Update the network firmware:

   ```sh
   beam device update-firmware --network-firmware firmware-selected/pc02/pc02-3.30.0-net_core_app_update.bin
   ```

   Wait for success. App and network versions need not match.

## Switch the Node to USB

1. If the Node already reports `USB Locked: true` and `Connection Mode: USB`,
   proceed to provisioning. Otherwise, stop Beam:

   ```sh
   beam down
   ```

   Apply USB lock while Beam is stopped:

   ```sh
   beam device apply-usb-lock --serial NODE_SERIAL
   ```

   The Node reboots when its mode changes. If it remains off, power it on manually.
2. Start Beam:

   ```sh
   beam up
   ```

3. Wait for USB discovery, then inspect the Node:

   ```sh
   beam device info
   ```

   Check `USB Locked: true` and `Connection Mode: USB`. Device info can appear
   while the Node is off; confirm physical power before provisioning.

## Provision the Node in the workspace

1. Confirm the Node is physically powered on. Check the daemon connection:

   ```sh
   beam device network
   ```

   Expect your serial and `State: Connected`. If disconnected, connect explicitly:

   ```sh
   beam device connect --usb USB_PORT
   ```

   `Device is already connected` is acceptable here; proceed to provisioning.
2. Provision the connected Node:

   ```sh
   beam device provision --workspace-id WORKSPACE_ID
   ```

   Confirm the workspace. Require `Register device: complete`,
   `Apply traffic key: confirmed`, `Sync workspace defaults: complete`, and
   `Apply device settings: complete`. Retry if any task fails or is unconfirmed.
3. Allow reports to settle, then inspect the Node:

   ```sh
   beam device info
   ```

   Check the serial, USB lock, USB mode, firmware, and nonzero `Account Id`.
   Wait and repeat if reports still show earlier values.
4. Read settings from the daemon-connected Node:

   ```sh
   curl --fail-with-body --silent --show-error http://localhost:9091/api/device/daemon-settings
   ```

   Require `success: true` and your serial. Compare radio, backhaul, and satellite
   settings with the workspace profile. Provisioning preserves existing settings
   overrides. Custom frequencies can display `Radio Channel: Unknown`.
5. Check the service:

   ```sh
   beam status
   ```

   Expect `Beam service (user): Up`. Enabled lingering keeps it running after logout.

## Send a radio test message

1. Check `beam device network` reports `Connected`, then send an agreed test
   message to the workspace:

   ```sh
   beam message send --workspace-id WORKSPACE_ID --channels radio --content "Beam radio connection test"
   ```

2. Confirm receipt at another client or a radio delivery acknowledgment in the
   logs. A successful command exit alone does not confirm delivery.

## Override radio settings when required

Skip this section for normal provisioning. Use it only for an administrator-supplied
profile. Beam must be running with the intended Node powered on and connected.

1. Create `radio-settings.json`. Replace the placeholders; frequencies are integer Hz:

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

   Use the supplied enum values: for example, `RadioSpreadFactor05`,
   `RadioBandwidth500KHz`, and `PowerModeHigh`. Keep only the intended Node attached.
2. Apply the profile through the local Beam API:

   ```sh
   curl --fail-with-body --silent --show-error --max-time 60 --header 'Content-Type: application/json' --data-binary @radio-settings.json http://localhost:9091/api/device/daemon-settings
   ```

   Require `success: true` and your serial. This also saves a workspace settings override.
3. Read the device settings back:

   ```sh
   curl --fail-with-body --silent --show-error http://localhost:9091/api/device/daemon-settings
   ```

   Check the returned profile matches the requested values.

## Troubleshooting

- **No devices found or account is 0:** check power, cable, and serial permissions.
  Wait for discovery, connect, then retry provisioning.
- **Already connected:** proceed to provisioning.
- **Beam daemon is running:** stop Beam before DFU or USB lock.
- **Peer client is down after startup:** wait, then repeat `beam status`.
- **USB port exists but Beam is disconnected:** reconnect with
  `beam device connect --usb USB_PORT`. Automatic connection may not recover
  this serial failure. Check queued messages before sending duplicates.
- **Settings differ:** ask your admin to check saved overrides. Grid host reset
  does not reset workspace settings.

## Related

- [Installer](install.sh) — the installer included in this bundle.
- `beam config reset-defaults --help` — Grid host configuration options.
- `beam device provision --help` — workspace provisioning options.
- `beam device --help` — device and firmware commands.
- `beam log --help` — inspect diagnostic logs.
