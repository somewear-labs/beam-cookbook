APP        := beam-ops
GCS_BUCKET := gs://get-somewear-app/atakplugin

DEVICES_ARM64     := tank@100.68.160.75 jahmelon@100.68.160.77
DEVICES_MAC_ARM64 := matthewroberts@mac.lan
BEAM_JAR          := ../beam/app/build/libs/somewear-beam.jar

.PHONY: package-mac package-linux ship ship-rpc ship-local ship-beam-jar clean

# Build macOS artifacts (.zip for arm64 and x64). Run on macOS.
package-mac:
	npm run package:mac

# Build Linux artifacts (.AppImage for arm64 and x64).
# Must run on Linux — better-sqlite3 is a native module and cannot be
# cross-compiled from macOS without Docker.
package-linux:
	npm run package:linux

# Upload all artifacts and the installer script to GCS.
# Assumes both package-mac and package-linux have been run (on their
# respective platforms) and their outputs are present in dist-electron/.
ship: ship-rpc
	gsutil -m cp -a public-read \
		dist-electron/$(APP)_mac_arm64.zip \
		dist-electron/$(APP)_mac_x64.zip \
		dist-electron/$(APP)_linux_arm64.AppImage \
		dist-electron/$(APP)_linux_x86_64.AppImage \
		"$(GCS_BUCKET)/"
	gsutil cp -a public-read install-beam-ops.sh "$(GCS_BUCKET)/install-beam-ops.sh"
	@echo "Published to https://get.somewear.app/atakplugin/"

# Upload rpc binaries and installer to GCS so install-rpc.sh works.
ship-rpc:
	gsutil -m cp -a public-read \
		../../rpc/bin/rpc_darwin_arm64 \
		../../rpc/bin/rpc_darwin_amd64 \
		../../rpc/bin/rpc_linux_arm64 \
		../../rpc/bin/rpc_linux_amd64 \
		"$(GCS_BUCKET)/"
	gsutil cp -a public-read ../../rpc/install-rpc.sh "$(GCS_BUCKET)/install-rpc.sh"
	@echo "rpc published to https://get.somewear.app/atakplugin/"

# Deploy directly to local SSH devices (no GCS required).
# Also deploys the rpc binary straight into each device's app userData directory.
# This sidesteps the extraResources cross-compilation bug (Linux AppImages built
# on macOS don't reliably include extraResources in the final squashfs).
ship-local:
	@for host in $(DEVICES_ARM64); do \
		echo "→ deploying to $$host"; \
		rsync -az --progress \
			dist-electron/$(APP)_linux_arm64.AppImage \
			$$host:~/bin/beam-ops; \
		ssh $$host "chmod +x ~/bin/beam-ops"; \
		rsync -az ../../rpc/bin/rpc_linux_arm64 $$host:~/bin/rpc; \
		ssh $$host "chmod +x ~/bin/rpc"; \
		echo "  done $$host"; \
	done
	@for host in $(DEVICES_MAC_ARM64); do \
		echo "→ deploying to $$host (mac)"; \
		rsync -az dist-electron/$(APP)_mac_arm64.zip $$host:/tmp/beam-ops.zip; \
		ssh $$host "unzip -o /tmp/beam-ops.zip -d /Applications && rm /tmp/beam-ops.zip"; \
		rsync -az ../../rpc/bin/rpc_darwin_arm64 $$host:~/bin/rpc; \
		ssh $$host "chmod +x ~/bin/rpc"; \
		ssh $$host 'pkill -x "Beam Ops" 2>/dev/null; sleep 1; open "/Applications/Beam Ops.app"'; \
		echo "  done $$host"; \
	done
	@echo "→ deploying locally (mac)"
	@unzip -o dist-electron/$(APP)_mac_arm64.zip -d /Applications > /dev/null 2>&1; true
	@mkdir -p ~/bin && cp ../../rpc/bin/rpc_darwin_arm64 ~/bin/rpc && chmod +x ~/bin/rpc
	@pkill -x "Beam Ops" 2>/dev/null; sleep 1; open "/Applications/Beam Ops.app"
	@echo "  done local"

# Push the local beam dev jar to all SSH devices and restart the daemon.
ship-beam-jar:
	@for host in $(DEVICES_ARM64); do \
		echo "→ beam.jar → $$host"; \
		rsync -az $(BEAM_JAR) $$host:/tmp/beam.jar; \
		ssh $$host "cp /tmp/beam.jar /usr/bin/beam.jar && rm /tmp/beam.jar && beam daemon stop; sleep 2; beam daemon start &"; \
		echo "  done $$host"; \
	done
	@for host in $(DEVICES_MAC_ARM64); do \
		echo "→ beam.jar → $$host (mac)"; \
		rsync -az $(BEAM_JAR) $$host:/tmp/beam.jar; \
		ssh $$host "cp /tmp/beam.jar ~/bin/beam.jar && rm /tmp/beam.jar && ~/bin/beam daemon stop; sleep 2; ~/bin/beam daemon start &"; \
		echo "  done $$host"; \
	done

clean:
	rm -rf dist-electron
