# Wizard installation

`install.sh` installs the wizard itself. It selects the host's GCS or UxS
bundle, resumes interrupted downloads, verifies the checksum, extracts the
release, and installs `grid-uxs-wizard` on the normal user's PATH.

`--install-only` stops after wizard installation. Otherwise the script hands off
to `grid-uxs-wizard setup` or `grid-uxs-wizard update`. Beam/TVPN setup
is implemented in the [Go CLI](../cmd/grid-uxs-wizard/).

Publish this directory's `install.sh` at `$FILE_SERVER/install.sh`, beside the
release archives and checksum sidecars. Both GCS and UxS archives contain the
same command name and `grid-uxs-wizard.json` config filename.

From the module root, run `go test ./bootstrap` to test the downloader, PATH
installation, archive validation, and terminal handoff.
