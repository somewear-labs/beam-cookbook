# Wizard entry point

Initial setup asks whether the target installation is a GCS or UxS and saves
the role. Later commands and menus infer it:

```sh
grid-uxs-wizard setup
grid-uxs-wizard provision-uxs
grid-uxs-wizard resume-claim
```

The provisioning commands are available on a configured GCS. On the Linux
mission computer, run setup and enter its API key and setup PIN. Automated
initial setup can pass `--role gcs` or `--role uxs`.

Provisioning commands run as the normal user. Initial system installation
requires elevation; Beam runs in user scope and TVPN runs in system scope.
The internal `run` command supervises the installed UxS runtime and is invoked
by its service, including before initial setup has finished saving its role.

Build from the module root with
`go build -o bin/grid-uxs-wizard ./cmd/grid-uxs-wizard`.
Implementation and tests are in [`internal/wizard/`](../../internal/wizard/).
