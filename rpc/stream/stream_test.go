package stream

import (
	"bytes"
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

const gridStreamTestTimeout = 5 * time.Second

func TestGridStreamRoundTrip(t *testing.T) {
	left, right := linkedGridStreamEndpoints()
	request := bytes.Repeat([]byte("grid-stream-request-"), 1024)
	response := bytes.Repeat([]byte("atak-log-line\n"), 1024)
	serverDone := make(chan struct{})
	serverStream := make(chan *Stream, 1)

	if err := right.Register("atak.logs.v1", func(stream *Stream) {
		defer close(serverDone)
		serverStream <- stream
		got, err := io.ReadAll(stream)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if !bytes.Equal(got, request) {
			t.Errorf("server request mismatch: got %d bytes, want %d", len(got), len(request))
			return
		}
		if _, err := stream.Write(response); err != nil {
			t.Errorf("server write: %v", err)
			return
		}
		if err := stream.CloseWithCode(7, "test status"); err != nil {
			t.Errorf("server close: %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gridStreamTestTimeout)
	defer cancel()
	stream, err := left.Open(ctx, 2, "atak.logs.v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(request); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	targetStream := <-serverStream
	type readResult struct {
		data []byte
		err  error
	}
	readDone := make(chan readResult, 1)
	go func() {
		data, readErr := io.ReadAll(stream)
		readDone <- readResult{data: data, err: readErr}
	}()
	var got []byte
	select {
	case result := <-readDone:
		got, err = result.data, result.err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(gridStreamTestTimeout):
		stream.mu.Lock()
		t.Logf("client recv=%d buffered=%d pending=%d final=%v", stream.receiveOffset, stream.readBuffer.Len(), stream.pendingBytes, stream.remoteFinal)
		stream.mu.Unlock()
		targetStream.mu.Lock()
		t.Logf("server send=%d acked=%d window=%d", targetStream.sendOffset, targetStream.ackedOffset, targetStream.peerWindow)
		targetStream.mu.Unlock()
		t.Fatal("stream response timed out")
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("client response mismatch: got %d bytes, want %d", len(got), len(response))
	}
	if code, message, closed := stream.CloseStatus(); !closed || code != 7 || message != "test status" {
		t.Fatalf("CloseStatus() = (%d, %q, %v)", code, message, closed)
	}
	select {
	case <-serverDone:
	case <-time.After(gridStreamTestTimeout):
		t.Fatal("server handler did not finish")
	}
}

func TestGridStreamReordersAndDeduplicatesData(t *testing.T) {
	var sent []*rpcpb.GridStreamFrame
	endpoint := NewEndpoint(func(_ int64, frame *rpcpb.GridStreamFrame) error {
		sent = append(sent, frame)
		return nil
	})
	stream := newStream(endpoint, 2, 7, "test", false, gridStreamDefaultWindow)
	endpoint.streams[stream.key()] = stream

	stream.handleData(&rpcpb.GridStreamData{Offset: 3, Payload: []byte("def")})
	stream.handleData(&rpcpb.GridStreamData{Offset: 0, Payload: []byte("abc")})
	stream.handleData(&rpcpb.GridStreamData{Offset: 0, Payload: []byte("abc")})
	stream.handleRemoteHalfClose(6)

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdef" {
		t.Fatalf("ReadAll() = %q", got)
	}
	if len(sent) < 3 {
		t.Fatalf("sent %d acknowledgements, want at least 3", len(sent))
	}
	if ack := sent[len(sent)-1].GetAck(); ack.GetReceivedOffset() != 6 || ack.GetReceiveWindowBytes() != gridStreamDefaultWindow {
		t.Fatalf("final ack = %+v", ack)
	}
}

func TestGridStreamDuplicateOpenRunsHandlerOnce(t *testing.T) {
	left, right := linkedGridStreamEndpoints()
	var calls atomic.Int32
	handlerStarted := make(chan struct{}, 1)
	if err := right.Register("test", func(stream *Stream) {
		calls.Add(1)
		handlerStarted <- struct{}{}
		<-stream.Done()
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gridStreamTestTimeout)
	stream, err := left.Open(ctx, 2, "test")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(gridStreamTestTimeout):
		t.Fatal("handler did not start")
	}

	right.Handle(1, &rpcpb.GridStreamFrame{
		StreamId: stream.streamID,
		Payload: &rpcpb.GridStreamFrame_Open{Open: &rpcpb.GridStreamOpen{
			ProtocolVersion:    gridStreamProtocolVersion,
			Service:            "test",
			ReceiveWindowBytes: gridStreamDefaultWindow,
		}},
	})
	time.Sleep(10 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler called %d times", got)
	}
	stream.Reset(ResetCancelled, "test complete")
}

func TestGridStreamRejectsOversizedData(t *testing.T) {
	left, right := linkedGridStreamEndpoints()
	handlerStarted := make(chan *Stream, 1)
	if err := right.Register("test", func(stream *Stream) { handlerStarted <- stream }); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gridStreamTestTimeout)
	stream, err := left.Open(ctx, 2, "test")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	peer := <-handlerStarted
	peer.handleData(&rpcpb.GridStreamData{Payload: make([]byte, gridStreamMaxDataBytes+1)})
	select {
	case <-stream.Done():
	case <-time.After(gridStreamTestTimeout):
		t.Fatal("oversized data did not reset peer")
	}
}

func linkedGridStreamEndpoints() (*Endpoint, *Endpoint) {
	var left, right *Endpoint
	left = NewEndpoint(func(_ int64, frame *rpcpb.GridStreamFrame) error {
		right.Handle(1, frame)
		return nil
	})
	right = NewEndpoint(func(_ int64, frame *rpcpb.GridStreamFrame) error {
		left.Handle(2, frame)
		return nil
	})
	return left, right
}
