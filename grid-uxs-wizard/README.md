# Grid Networked UxS wizard

One executable installs Beam and TVPN on a GCS or Linux mission computer.
Run `grid-uxs-wizard setup` to choose and remember this computer's role.
Setup requests administrator permission; Beam runs as the normal user and TVPN
runs as a system service.

On a configured GCS, `grid-uxs-wizard provision-uxs` provisions an aircraft;
`grid-uxs-wizard resume-claim` continues an interrupted setup. Enter the UxS
API key and six-digit setup PIN on the mission computer. The wizard discovers
and verifies the GCS over TVPN before sending the PIN. Extending the setup
window in the GCS fleet manager keeps the same PIN and identity. Saved
installations can resume without entering another PIN.

Run the wizard without arguments to choose from this installation's tasks.

The CLI entry point is in `cmd/grid-uxs-wizard/`. Implementation and tests live
in `internal/wizard/`.

Build and test from this directory:

```sh
go build -o bin/grid-uxs-wizard ./cmd/grid-uxs-wizard
go test ./...
```

On Linux mission computers, `grid-uxs-wizard reset --backup` preserves a private
backup before resetting local services and state. It leaves the GCS and backend
identity unchanged. Omit `--backup` to reset without retaining a backup.
