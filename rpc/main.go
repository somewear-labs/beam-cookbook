package main

import (
	"fmt"
	"os"
)

const usage = `usage: rpc <command> [options]

Commands:
  server   Run the RPC webhook server (on the remote machine)
  nmap     Discover Grid Remote Shell targets in the workspace
  shell    Start an interactive remote shell (on the local machine)
  send     Send a single command

Run 'rpc <command> -help' for command-specific options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "server":
		runServer(os.Args[2:])
	case "nmap":
		runNmap(os.Args[2:])
	case "shell":
		runShell(os.Args[2:])
	case "send":
		runSend(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}
