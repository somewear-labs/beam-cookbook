package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	rpcpb "somewear/rpc/proto"
)

func runSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	workspace := fs.Int("workspace", defaultWorkspaceID, "Somewear workspace ID")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL")
	fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: rpc send [--workspace N] <command>")
		os.Exit(1)
	}

	command := strings.Join(fs.Args(), " ")
	env := &rpcpb.Envelope{
		RequestId: 1,
		Payload: &rpcpb.Envelope_Request{
			Request: &rpcpb.RpcRequest{
				Method: &rpcpb.RpcRequest_Exec{
					Exec: &rpcpb.ExecRequest{Command: command},
				},
			},
		},
	}

	b64, err := marshalEnvelope(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode error:", err)
		os.Exit(1)
	}
	if err := sendIPv4(*beamURL, *workspace, b64); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("Command sent.")
}
