---
title: "Beam install kit"
description: "Install Beam and provision a Node using the release bundle."
audience: "For developers and integrators setting up Beam on Linux."
lastUpdated: 2026-10-03
---

Follow [Getting started](GETTING_STARTED.md) to install Beam, apply Grid host
defaults, and provision a powered-on Node in your workspace.

## Use the release bundle

Download the complete release bundle supplied by your administrator. The
installer and launcher need its Beam JAR, checksum manifest, and dependencies;
this cookbook directory alone is not an installation package.

Run the guide's commands one at a time and check each result. Firmware updates
are optional when the Node already has the supplied release. Provisioning
registers the Node, confirms the workspace traffic key, and applies effective
workspace settings.

## Related

- [Getting started](GETTING_STARTED.md) — installation through a radio test.
- [Installer](install.sh) — install the files from the complete bundle.
- [Cookbook recipes](../README.md) — build on the running Beam service.
