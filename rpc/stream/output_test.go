package stream

import (
	"context"
	"io"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

func TestCommandOutputFlowsFromTargetToClient(t *testing.T) {
	client, target := linkedGridStreamEndpoints()
	received := make(chan *Stream, 1)
	if err := RegisterCommandOutput(client, func(stream *Stream) {
		received <- stream
	}); err != nil {
		t.Fatal(err)
	}
	route := Route{SessionID: 7, Channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	opened := make(chan *Stream, 1)
	go func() {
		stream, err := OpenCommandOutput(ctx, target, 1, route)
		if err != nil {
			t.Error(err)
			return
		}
		opened <- stream
	}()

	targetOutput := <-opened
	clientOutput := <-received
	if _, err := targetOutput.Write([]byte("line one\n")); err != nil {
		t.Fatal(err)
	}
	if err := targetOutput.CloseWithCode(0, ""); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(clientOutput)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "line one\n" {
		t.Fatalf("output = %q", got)
	}
}
