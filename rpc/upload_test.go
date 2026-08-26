package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	rpcpb "somewear/rpc/proto"
)

func TestUploadFileRPCSendsOneRouterFile(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(localPath, []byte("hello grid"), 0o640); err != nil {
		t.Fatal(err)
	}

	var nextID, pendingID atomic.Uint32
	responses := make(chan inboundEnvelope, 1)
	route := sessionRoute{id: 17, channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}}
	send := func(_ string, workspace int, targetUserID int64, path string, channels []rpcpb.SessionChannel) error {
		if workspace != 7 || targetUserID != 42 || !sameSessionChannels(channels, route.channels) {
			t.Fatalf("route = workspace %d, target %d, channels %v", workspace, targetUserID, channels)
		}
		wireBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var envelope rpcpb.Envelope
		if err := proto.Unmarshal(wireBytes, &envelope); err != nil {
			return err
		}
		put := envelope.GetRequest().GetPut()
		if envelope.GetNamespace() != EnvelopeNamespace || envelope.GetSessionId() != route.id ||
			put.GetName() != "hello.txt" || string(put.GetData()) != "hello grid" || !put.GetComplete() {
			t.Fatalf("upload envelope = %+v", &envelope)
		}
		responses <- inboundEnvelope{envelope: &rpcpb.Envelope{
			RequestId: envelope.GetRequestId(),
			SessionId: route.id,
			Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Put{Put: &rpcpb.PutResponse{
					NextOffset: uint64(len("hello grid")),
					Path:       "/remote/hello.txt",
					Complete:   true,
				}},
			}},
		}}
		return nil
	}

	remotePath, err := uploadFileRPC(
		"beam", 7, 42, route, localPath, time.Second,
		&nextID, &pendingID, responses, send,
	)
	if err != nil {
		t.Fatal(err)
	}
	if remotePath != "/remote/hello.txt" || pendingID.Load() != 0 {
		t.Fatalf("result = path %q, pending %d", remotePath, pendingID.Load())
	}
}

func TestApplyPutChunkUploadsFileAndAcceptsRetry(t *testing.T) {
	server := &rpcServer{uploadRoot: t.TempDir()}
	first := &rpcpb.PutRequest{
		TransferId: "transfer-1",
		Name:       "hello.sh",
		Mode:       0o700,
		TotalSize:  11,
		Data:       []byte("hello "),
	}
	response, err := server.applyPutChunk(42, first)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetNextOffset() != 6 || response.GetComplete() {
		t.Fatalf("first response = %+v", response)
	}

	retry, err := server.applyPutChunk(42, first)
	if err != nil {
		t.Fatal(err)
	}
	if retry.GetNextOffset() != 6 || retry.GetComplete() {
		t.Fatalf("retry response = %+v", retry)
	}

	last := &rpcpb.PutRequest{
		TransferId: "transfer-1",
		Name:       "hello.sh",
		Mode:       0o700,
		Offset:     6,
		TotalSize:  11,
		Data:       []byte("world"),
		Complete:   true,
	}
	response, err = server.applyPutChunk(42, last)
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetComplete() || response.GetNextOffset() != 11 {
		t.Fatalf("final response = %+v", response)
	}
	wantPath := filepath.Join(server.uploadRoot, "42", "hello.sh")
	if response.GetPath() != wantPath {
		t.Fatalf("path = %q, want %q", response.GetPath(), wantPath)
	}
	contents, err := os.ReadFile(wantPath)
	if err != nil || string(contents) != "hello world" {
		t.Fatalf("uploaded contents = %q, %v", contents, err)
	}
	info, err := os.Stat(wantPath)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("uploaded mode = %v, %v", info, err)
	}

	retry, err = server.applyPutChunk(42, last)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.GetComplete() || retry.GetPath() != wantPath {
		t.Fatalf("final retry response = %+v", retry)
	}
}

func TestApplyPutChunkRejectsTraversalAndOversizedData(t *testing.T) {
	server := &rpcServer{uploadRoot: t.TempDir()}
	for name, request := range map[string]*rpcpb.PutRequest{
		"traversal": {
			TransferId: "transfer-1", Name: "../escape", Complete: true,
		},
		"oversized data": {
			TransferId: "transfer-2", Name: "large", TotalSize: rpcUploadMaxSize + 1,
			Data: make([]byte, rpcUploadMaxSize+1), Complete: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := server.applyPutChunk(42, request); err == nil {
				t.Fatal("applyPutChunk() succeeded")
			}
		})
	}
}
