package main

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	rpcpb "somewear/rpc/proto"
)

const sessionTTL = 24 * time.Hour

var sessionChannelNames = map[rpcpb.SessionChannel]string{
	rpcpb.SessionChannel_RADIO:     "Radio",
	rpcpb.SessionChannel_SATELLITE: "Satellite",
	rpcpb.SessionChannel_CELLULAR:  "Cellular",
	rpcpb.SessionChannel_MESH:      "Mesh",
}

type sessionRoute struct {
	id       uint64
	channels []rpcpb.SessionChannel
}

func parseSessionChannels(value string) ([]rpcpb.SessionChannel, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("channel list must not be empty")
	}
	byName := make(map[string]rpcpb.SessionChannel, len(sessionChannelNames))
	for channel, name := range sessionChannelNames {
		byName[strings.ToLower(name)] = channel
	}

	seen := make(map[rpcpb.SessionChannel]bool)
	for _, part := range strings.Split(value, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		channel, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown channel %q; use radio, satellite, cellular, or mesh", strings.TrimSpace(part))
		}
		if seen[channel] {
			return nil, fmt.Errorf("duplicate channel %q", strings.TrimSpace(part))
		}
		seen[channel] = true
	}

	channels := make([]rpcpb.SessionChannel, 0, len(seen))
	for channel := range seen {
		channels = append(channels, channel)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	return channels, nil
}

func validateSessionChannels(channels []rpcpb.SessionChannel) ([]rpcpb.SessionChannel, error) {
	if len(channels) == 0 {
		return nil, errors.New("session requires at least one channel")
	}
	seen := make(map[rpcpb.SessionChannel]bool, len(channels))
	canonical := make([]rpcpb.SessionChannel, 0, len(channels))
	for _, channel := range channels {
		if _, ok := sessionChannelNames[channel]; !ok {
			return nil, fmt.Errorf("unsupported session channel: %s", channel)
		}
		if seen[channel] {
			return nil, fmt.Errorf("duplicate session channel: %s", channel)
		}
		seen[channel] = true
		canonical = append(canonical, channel)
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i] < canonical[j] })
	return canonical, nil
}

func formatSessionChannels(channels []rpcpb.SessionChannel) string {
	names := make([]string, 0, len(channels))
	for _, channel := range channels {
		if name, ok := sessionChannelNames[channel]; ok {
			names = append(names, strings.ToLower(name))
		}
	}
	return strings.Join(names, ",")
}

func beamChannelNames(channels []rpcpb.SessionChannel) []string {
	names := make([]string, 0, len(channels))
	for _, channel := range channels {
		if name, ok := sessionChannelNames[channel]; ok {
			names = append(names, name)
		}
	}
	return names
}

func randomSessionID() uint64 {
	var value [8]byte
	if _, err := rand.Read(value[:]); err == nil {
		if id := binary.LittleEndian.Uint64(value[:]); id != 0 {
			return id
		}
	}
	return uint64(time.Now().UnixNano()) | 1
}

func sameSessionChannels(left, right []rpcpb.SessionChannel) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type sessionKey struct {
	peerAccountID int64
	sessionID     uint64
}

type sessionState struct {
	channels   []rpcpb.SessionChannel
	lastActive time.Time
}

type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[sessionKey]sessionState
	now      func() time.Time
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[sessionKey]sessionState), now: time.Now}
}

func (r *sessionRegistry) put(peerAccountID int64, route sessionRoute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	r.sessions[sessionKey{peerAccountID: peerAccountID, sessionID: route.id}] = sessionState{
		channels:   append([]rpcpb.SessionChannel(nil), route.channels...),
		lastActive: r.now(),
	}
}

func (r *sessionRegistry) get(peerAccountID int64, sessionID uint64) (sessionRoute, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	key := sessionKey{peerAccountID: peerAccountID, sessionID: sessionID}
	state, ok := r.sessions[key]
	if !ok {
		return sessionRoute{}, false
	}
	state.lastActive = r.now()
	r.sessions[key] = state
	return sessionRoute{id: sessionID, channels: append([]rpcpb.SessionChannel(nil), state.channels...)}, true
}

func (r *sessionRegistry) remove(peerAccountID int64, sessionID uint64) {
	r.mu.Lock()
	delete(r.sessions, sessionKey{peerAccountID: peerAccountID, sessionID: sessionID})
	r.mu.Unlock()
}

func (r *sessionRegistry) expireLocked() {
	now := r.now()
	for key, state := range r.sessions {
		if now.Sub(state.lastActive) >= sessionTTL {
			delete(r.sessions, key)
		}
	}
}
