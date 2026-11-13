package main

const wizardUsage = `Grid Networked UxS setup

Run without arguments for guided setup:

  grid-uxs-wizard

On a Linux mission computer, run with sudo. Choose GCS claim creation,
UxS setup, or GCS installation from the menu.

Supply primary options to skip their prompts:

  grid-uxs-wizard prepare-claim --name <aircraft-name>

  sudo ./grid-uxs-wizard setup --role uxs --gcs <GCS-HTTPS-address>

The wizard reads grid-uxs-wizard.json beside its executable for the file server.
Installation files and UxS state are selected automatically.

Other commands:
  resume-claim       Show or finish the pending GCS claim
  setup --role gcs    Install Beam and TVPN on the GCS

Advanced presentation options:
  --non-interactive  Require flags instead of asking questions
  --json-progress    Emit structured progress events instead of readable steps

Run a command with --help to see advanced options.
`

const uxsUsage = `Run on the Linux mission computer after extracting the complete UxS download:

  sudo ./grid-uxs-wizard setup --role uxs

The wizard asks for the GCS HTTPS address and your claim code. Create the code
on the GCS with:

  grid-uxs-wizard prepare-claim

Approve in the browser, then paste the entire displayed code on the mission computer.
The GCS address must be reachable directly; it is not the file-server address.
The wizard finds bundle/ beside its executable and selects managed state.

Advanced options and prompt overrides:
`

const claimUsage = `Run on the GCS with Beam signed in to the intended Workspace:

  grid-uxs-wizard prepare-claim

The wizard asks for a UxS name, fetches Beam from your configured file server,
opens browser approval, and displays the claim code when approved.
Use your Organization Admin account, then paste the entire code into UxS setup.

Advanced options and prompt overrides:
`
