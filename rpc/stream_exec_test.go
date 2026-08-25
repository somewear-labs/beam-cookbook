package main

import (
	"io"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
	"somewear/rpc/stream"
)

func TestStreamExecUsesRequestInitiatedTargetOutput(t *testing.T) {
	const (
		clientAccountID = int64(7)
		targetAccountID = int64(11)
	)

	var client, target *stream.Endpoint
	client = stream.NewEndpoint(func(_ int64, route stream.Route, _ bool, frame *rpcpb.GridStreamFrame) error {
		target.Handle(clientAccountID, route, frame)
		return nil
	})
	target = stream.NewEndpoint(func(_ int64, route stream.Route, _ bool, frame *rpcpb.GridStreamFrame) error {
		client.Handle(targetAccountID, route, frame)
		return nil
	})

	received := make(chan *stream.Stream, 1)
	if err := stream.RegisterCommandOutput(client, func(output *stream.Stream) {
		received <- output
	}); err != nil {
		t.Fatal(err)
	}

	server := &rpcServer{
		cwd:      t.TempDir(),
		sessions: newSessionRegistry(),
		streams:  target,
	}
	done := make(chan struct{})
	go func() {
		server.handleStreamExec(1, clientAccountID, sessionRoute{}, &rpcpb.ExecRequest{
			Command:      "printf 'hello from target'",
			StreamOutput: true,
		})
		close(done)
	}()

	var output *stream.Stream
	select {
	case output = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive target-originated command output stream")
	}
	got, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello from target" {
		t.Fatalf("output = %q", got)
	}
	if exitCode, message, ok := output.CloseStatus(); !ok || exitCode != 0 || message != "" {
		t.Fatalf("close status = (%d, %q, %t)", exitCode, message, ok)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streaming command did not finish")
	}
}
