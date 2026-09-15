package main

import (
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

func TestRequestIdempotencyRejectsDuplicateWithinSourceAndSession(t *testing.T) {
	guard := idempotencyGuard{}
	request := execEnvelope(7, 11, "first")

	if !guard.acceptRequest(42, request) {
		t.Fatal("first request was rejected")
	}
	if guard.acceptRequest(42, request) {
		t.Fatal("duplicate request was accepted")
	}
	// A request ID is the operation's idempotency token. Reusing it with a
	// different body must not execute a second operation.
	if guard.acceptRequest(42, execEnvelope(7, 11, "different")) {
		t.Fatal("reused request ID with different content was accepted")
	}
	if !guard.acceptRequest(43, request) {
		t.Fatal("same request ID from another source was rejected")
	}
	if !guard.acceptRequest(42, execEnvelope(7, 12, "first")) {
		t.Fatal("same request ID in another session was rejected")
	}
}

func TestRequestIdempotencyAllowsZeroRequestID(t *testing.T) {
	guard := idempotencyGuard{}
	request := execEnvelope(0, 0, "disconnect-like control")
	if !guard.acceptRequest(42, request) || !guard.acceptRequest(42, request) {
		t.Fatal("request ID zero should remain fire-and-forget")
	}
}

func TestResponseIdempotencyRejectsOnlyIdenticalResponses(t *testing.T) {
	guard := idempotencyGuard{}
	first := execResponseEnvelope(7, 11, "first")

	if !guard.acceptResponse(42, first) {
		t.Fatal("first response was rejected")
	}
	if guard.acceptResponse(42, first) {
		t.Fatal("identical response was accepted twice")
	}
	// Distinct frames may share a request ID once streaming responses are
	// layered onto this branch, so include response content in the key.
	if !guard.acceptResponse(42, execResponseEnvelope(7, 11, "second")) {
		t.Fatal("distinct response content was rejected")
	}
}

func TestIdempotencyEntriesExpire(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	guard := idempotencyGuard{now: func() time.Time { return now }}
	request := execEnvelope(7, 11, "command")

	if !guard.acceptRequest(42, request) || guard.acceptRequest(42, request) {
		t.Fatal("request was not guarded before expiry")
	}
	now = now.Add(idempotencyTTL)
	if !guard.acceptRequest(42, request) {
		t.Fatal("request was not accepted after expiry")
	}
}

func execEnvelope(requestID uint32, sessionID uint64, command string) *rpcpb.Envelope {
	return &rpcpb.Envelope{
		RequestId: requestID,
		SessionId: sessionID,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Exec{Exec: &rpcpb.ExecRequest{Command: command}},
		}},
	}
}

func execResponseEnvelope(requestID uint32, sessionID uint64, output string) *rpcpb.Envelope {
	return &rpcpb.Envelope{
		RequestId: requestID,
		SessionId: sessionID,
		Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
			Result: &rpcpb.RpcResponse_Exec{Exec: &rpcpb.ExecResponse{Output: output}},
		}},
	}
}
