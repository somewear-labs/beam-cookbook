package wizard

const wizardUsage = `Grid Networked UxS wizard

Start initial setup:

  grid-uxs-wizard setup

Setup asks whether the installation is a GCS or UxS mission computer and
remembers that role. Later commands and menus use the installed role.

After GCS setup:
  provision-uxs      Provision a UxS and generate its setup PIN
  resume-claim       Continue UxS provisioning

Run without arguments for guided setup or the installed role's task menu.
Presentation options: --non-interactive, --json-progress.
`

const setupUsage = `Set up Beam and TVPN:

  grid-uxs-wizard setup

Initial setup asks whether the target is a GCS or UxS mission computer.
Later setup runs reuse the installed role. For automation, initial setup
accepts --role gcs or --role uxs. --dry-run does not save a role.

The current target is local. Remote targets are not implemented yet.
Run --help after setup to see commands for the installed role.
`

const uxsUsage = `Run on the Linux mission computer after extracting the complete UxS download:

  grid-uxs-wizard setup

Choose UxS during initial setup. The wizard remembers this installation's role.
Enter the UxS API key created on the GCS with:

  grid-uxs-wizard provision-uxs

Enter the API key and six-digit setup PIN first. The wizard then connects through TVPN and verifies the GCS.
The wizard finds bundle/ beside its executable and selects managed state.
An existing installation resumes its saved pairing without another PIN.

Advanced options and prompt overrides:
`

const claimUsage = `Run on the configured GCS with Beam signed in to the intended Workspace:

  grid-uxs-wizard provision-uxs

Enter a UxS name and approve with your Organization Admin account in the
browser. The wizard displays a setup PIN and UxS API key, and saves a private
key copy. Enter both values in setup on the mission computer.

Advanced options and prompt overrides:
`

const gcsWizardUsage = `Grid Networked UxS wizard — GCS installation

Run without arguments to choose a task:

  grid-uxs-wizard

Commands:
  provision-uxs      Provision a UxS and generate its setup PIN
  resume-claim       Continue UxS provisioning
  setup              Set up Beam and TVPN using this installation's saved GCS role

Setup requests administrator permission for TVPN and system services. Provisioning commands run as your
normal user. Presentation options: --non-interactive, --json-progress.
Run a command with --help for its options.
`

const uxsWizardUsage = `Grid Networked UxS wizard — mission computer installation

Run without arguments to choose a task:

  grid-uxs-wizard

Commands:
  setup              Set up Beam and TVPN using this installation's saved UxS role
  reset              Reset this mission computer; --backup keeps a private backup (requests administrator permission)
  run                Supervise the installed UxS runtime (used by its service)

Setup requests administrator permission for TVPN and system services. Presentation options:
--non-interactive, --json-progress. Run a command with --help for its options.
`
