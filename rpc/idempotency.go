package main

import (
	"crypto/sha256"
	"sync"
	"time"

	rpcpb "somewear/rpc/proto"

	"google.golang.org/protobuf/proto"
)

// Keep request IDs for the lifetime of a shell session. This is long enough to
// catch a delayed copy from another Grid channel without growing the registry
// indefinitely.
const idempotencyTTL = sessionTTL

type requestIdempotencyKey struct {
	sourceUserID int64
	sessionID    uint64
	requestID    uint32
}

type responseIdempotencyKey struct {
	requestIdempotencyKey
	fingerprint [sha256.Size]byte
}

// idempotencyGuard is safe for concurrent webhook handlers. Its zero value is
// ready to use, which keeps rpcServer test construction simple.
type idempotencyGuard struct {
	mu        sync.Mutex
	requests  map[requestIdempotencyKey]time.Time
	responses map[responseIdempotencyKey]time.Time
	now       func() time.Time
}

func (g *idempotencyGuard) acceptRequest(sourceUserID int64, env *rpcpb.Envelope) bool {
	// Request ID zero is reserved for fire-and-forget control messages such as
	// disconnect. Those operations are already harmless when repeated and do
	// not have a usable idempotency token.
	if env.GetRequestId() == 0 {
		return true
	}
	key := requestIdempotencyKey{
		sourceUserID: sourceUserID,
		sessionID:    env.GetSessionId(),
		requestID:    env.GetRequestId(),
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.timeNow()
	g.expireLocked(now)
	if _, exists := g.requests[key]; exists {
		return false
	}
	if g.requests == nil {
		g.requests = make(map[requestIdempotencyKey]time.Time)
	}
	g.requests[key] = now
	return true
}

func (g *idempotencyGuard) acceptResponse(sourceUserID int64, env *rpcpb.Envelope) bool {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(env)
	if err != nil {
		// A response that cannot be fingerprinted will also fail normal protobuf
		// handling, so do not turn the guard into a separate failure mode.
		return true
	}
	key := responseIdempotencyKey{
		requestIdempotencyKey: requestIdempotencyKey{
			sourceUserID: sourceUserID,
			sessionID:    env.GetSessionId(),
			requestID:    env.GetRequestId(),
		},
		fingerprint: sha256.Sum256(encoded),
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.timeNow()
	g.expireLocked(now)
	if _, exists := g.responses[key]; exists {
		return false
	}
	if g.responses == nil {
		g.responses = make(map[responseIdempotencyKey]time.Time)
	}
	g.responses[key] = now
	return true
}

func (g *idempotencyGuard) timeNow() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *idempotencyGuard) expireLocked(now time.Time) {
	for key, seenAt := range g.requests {
		if now.Sub(seenAt) >= idempotencyTTL {
			delete(g.requests, key)
		}
	}
	for key, seenAt := range g.responses {
		if now.Sub(seenAt) >= idempotencyTTL {
			delete(g.responses, key)
		}
	}
}
