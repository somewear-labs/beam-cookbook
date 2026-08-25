package main

import (
	"os"
	"path/filepath"
	"testing"

	rpcpb "somewear/rpc/proto"
)

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

func TestApplyPutChunkRejectsTraversalAndOversizedChunks(t *testing.T) {
	server := &rpcServer{uploadRoot: t.TempDir()}
	for name, request := range map[string]*rpcpb.PutRequest{
		"traversal": {
			TransferId: "transfer-1", Name: "../escape", Complete: true,
		},
		"oversized chunk": {
			TransferId: "transfer-2", Name: "large", TotalSize: rpcUploadChunkSize + 1,
			Data: make([]byte, rpcUploadChunkSize+1), Complete: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := server.applyPutChunk(42, request); err == nil {
				t.Fatal("applyPutChunk() succeeded")
			}
		})
	}
}
