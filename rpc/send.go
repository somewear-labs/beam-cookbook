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
	workspace := fs.Int("workspace", 0, "Deprecated; GridDatagram uses Beam's active workspace")
	targetUserID := fs.Int64("target-user-id", 0, "Send to a specific user account ID instead of the whole workspace")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL")
	fs.Parse(args)
	if *workspace != 0 {
		fmt.Fprintln(os.Stderr, "send: --workspace is unavailable with GridDatagram; activate the workspace in Beam")
		os.Exit(1)
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: rpc send [--target-user-id N] <command>")
		os.Exit(1)
	}

	command := strings.Join(fs.Args(), " ")
	env := &rpcpb.Envelope{
		RequestId: randomRequestID(),
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
	if _, err := sendBeamDatagram(*beamURL, *targetUserID, b64, nil); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("Command sent.")
}
