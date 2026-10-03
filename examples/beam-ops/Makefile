APP          := beam-ops
GCS_BUCKET   := gs://get-somewear-app/atakplugin
GSUTIL_FLAGS := -h "Cache-Control:no-cache, no-store, must-revalidate"

HOSTS_CONF ?= $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../../../beam/script/dev-hosts.conf)
ifeq ($(wildcard $(HOSTS_CONF)),)
$(error Dev hosts file not found: $(HOSTS_CONF))
endif

DEVICES_ARM64     := $(shell awk 'NF >= 2 && substr($$1,1,1) != sprintf("%c",35) && $$2 == "linux" { print $$1 }' $(HOSTS_CONF))
DEVICES_MAC_ARM64 := $(shell awk 'NF >= 2 && substr($$1,1,1) != sprintf("%c",35) && $$2 == "mac" { print $$1 }' $(HOSTS_CONF))
DEVICES_LOCAL     := $(shell awk 'NF >= 2 && substr($$1,1,1) != sprintf("%c",35) && $$2 == "local" { print $$1 }' $(HOSTS_CONF))
BEAM_JAR          := ../../../beam/app/build/libs/somewear-beam.jar

.PHONY: package-mac package-linux ship ship-dev ship-rpc ship-local ship-beam-jar versions clean

# Full dev deploy from macOS:
#   1. Builds macOS arm64 + x64 .zip artifacts (package-mac)
#   2. Pushes local beam dev jar to all devices in DEVICES_ARM64 and DEVICES_MAC_ARM64;
#      restarts beam daemon on each
#   3. Deploys linux arm64 AppImage + rpc binary to DEVICES_ARM64 via rsync (uses
#      whatever AppImage is already in dist-electron/ — run package-linux on a Linux box first)
#   4. Deploys mac arm64 .zip + rpc binary to DEVICES_MAC_ARM64 via rsync; relaunches Beam Ops
#   5. Installs mac arm64 .zip to /Applications and rpc to ~/bin locally; relaunches Beam Ops
ship-dev: package-mac package-linux ship-local

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
	gsutil -m $(GSUTIL_FLAGS) cp -a public-read \
		dist-electron/$(APP)_mac_arm64.zip \
		dist-electron/$(APP)_mac_x64.zip \
		dist-electron/$(APP)_linux_arm64.AppImage \
		dist-electron/$(APP)_linux_x86_64.AppImage \
		install-beam-ops.sh \
		"$(GCS_BUCKET)/"
	@echo "Published to https://get.somewear.app/atakplugin/"

# Upload rpc binaries and installer to GCS so install-rpc.sh works.
ship-rpc:
	$(MAKE) -C ../../rpc build-all
	gsutil -m $(GSUTIL_FLAGS) cp -a public-read \
		../../rpc/bin/rpc_darwin_arm64 \
		../../rpc/bin/rpc_darwin_amd64 \
		../../rpc/bin/rpc_linux_arm64 \
		../../rpc/bin/rpc_linux_amd64 \
		../../rpc/install-rpc.sh \
		"$(GCS_BUCKET)/"
	@echo "rpc published to https://get.somewear.app/atakplugin/"

# Deploy directly to local SSH devices (no GCS required).
# Also deploys the rpc binary straight into each device's app userData directory.
# This sidesteps the extraResources cross-compilation bug (Linux AppImages built
# on macOS don't reliably include extraResources in the final squashfs).
ship-local: ship-beam-jar
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

# Print Beam and RPC versions from every active entry in beam/script/dev-hosts.conf.
versions:
	@failed=0; \
	printf '%-32s %-38s %s\n' HOST BEAM RPC; \
	printf '%-32s %-38s %s\n' '--------------------------------' '--------------------------------------' '--------------------------------------'; \
	query_versions() { \
		host="$$1"; os="$$2"; \
		case "$$os" in \
			linux) result=$$(ssh -o BatchMode=yes -o ConnectTimeout=8 "$$host" 'beam=$$(/usr/bin/beam -b version 2>&1) || beam="UNAVAILABLE"; rpc=$$(~/bin/rpc --version 2>&1) || rpc="UNAVAILABLE"; printf "%s|%s" "$$beam" "$$rpc"') || result='UNREACHABLE|UNREACHABLE' ;; \
			mac) result=$$(ssh -o BatchMode=yes -o ConnectTimeout=8 "$$host" 'beam=$$(~/bin/beam -b version 2>&1) || beam="UNAVAILABLE"; rpc=$$(~/bin/rpc --version 2>&1) || rpc="UNAVAILABLE"; printf "%s|%s" "$$beam" "$$rpc"') || result='UNREACHABLE|UNREACHABLE' ;; \
			local) beam=$$("$$HOME/bin/beam" -b version 2>&1) || beam=UNAVAILABLE; rpc=$$("$$HOME/bin/rpc" --version 2>&1) || rpc=UNAVAILABLE; result="$$beam|$$rpc" ;; \
		esac; \
		beam=$${result%%|*}; rpc=$${result#*|}; \
		printf '%-32s %-38s %s\n' "$$host ($$os)" "$$beam" "$$rpc"; \
		case "$$result" in *UNAVAILABLE*|*UNREACHABLE*) failed=1 ;; esac; \
	}; \
	for host in $(DEVICES_ARM64); do query_versions "$$host" linux; done; \
	for host in $(DEVICES_MAC_ARM64); do query_versions "$$host" mac; done; \
	for host in $(DEVICES_LOCAL); do query_versions "$$host" local; done; \
	exit $$failed

clean:
	rm -rf dist-electron
