package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	rpcpb "somewear/rpc/proto"
)

const (
	gridStreamProtocolVersion uint32 = 1
	gridStreamMaxDataBytes           = 280
	gridStreamDefaultWindow          = 8 * 1024
	gridStreamMaxWindow              = 64 * 1024
	gridStreamMaxServiceBytes        = 64
	gridStreamTombstoneTTL           = time.Minute
)

var errGridStreamClosed = errors.New("grid stream closed")

type ResetReason int32

const (
	ResetPeerUnavailable ResetReason = ResetReason(rpcpb.GridStreamReset_PEER_UNAVAILABLE)
	ResetProtocolError   ResetReason = ResetReason(rpcpb.GridStreamReset_PROTOCOL_ERROR)
	ResetCancelled       ResetReason = ResetReason(rpcpb.GridStreamReset_CANCELLED)
	ResetInternalError   ResetReason = ResetReason(rpcpb.GridStreamReset_INTERNAL_ERROR)
)

type gridStreamKey struct {
	peerAccountID int64
	streamID      uint32
}

type Frame = rpcpb.GridStreamFrame

type Sender func(peerAccountID int64, frame *Frame) error
type Handler func(*Stream)

type Endpoint struct {
	mu       sync.Mutex
	streams  map[gridStreamKey]*Stream
	closed   map[gridStreamKey]time.Time
	handlers map[string]Handler
	send     Sender
	nextID   atomic.Uint32
}

func NewEndpoint(send Sender) *Endpoint {
	endpoint := &Endpoint{
		streams:  make(map[gridStreamKey]*Stream),
		closed:   make(map[gridStreamKey]time.Time),
		handlers: make(map[string]Handler),
		send:     send,
	}
	endpoint.nextID.Store(randomRequestID())
	return endpoint
}

func (e *Endpoint) Register(service string, handler Handler) error {
	if service == "" || len(service) > gridStreamMaxServiceBytes {
		return fmt.Errorf("grid stream service must contain 1-%d bytes", gridStreamMaxServiceBytes)
	}
	if handler == nil {
		return errors.New("grid stream handler is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.handlers[service]; exists {
		return fmt.Errorf("grid stream service already registered: %s", service)
	}
	e.handlers[service] = handler
	return nil
}

func (e *Endpoint) Open(ctx context.Context, peerAccountID int64, service string) (*Stream, error) {
	if peerAccountID <= 0 {
		return nil, errors.New("grid stream peer account must be greater than zero")
	}
	if service == "" || len(service) > gridStreamMaxServiceBytes {
		return nil, fmt.Errorf("grid stream service must contain 1-%d bytes", gridStreamMaxServiceBytes)
	}

	var stream *Stream
	for {
		streamID := e.nextID.Add(1)
		if streamID == 0 {
			continue
		}
		candidate := newStream(e, peerAccountID, streamID, service, true, 0)
		key := candidate.key()
		e.mu.Lock()
		e.expireTombstonesLocked()
		_, recentlyClosed := e.closed[key]
		if _, exists := e.streams[key]; !exists && !recentlyClosed {
			e.streams[key] = candidate
			stream = candidate
		}
		e.mu.Unlock()
		if stream != nil {
			break
		}
	}

	err := stream.sendFrame(&rpcpb.GridStreamFrame{
		StreamId: stream.streamID,
		Payload: &rpcpb.GridStreamFrame_Open{
			Open: &rpcpb.GridStreamOpen{
				ProtocolVersion:    gridStreamProtocolVersion,
				Service:            service,
				ReceiveWindowBytes: gridStreamDefaultWindow,
			},
		},
	})
	if err != nil {
		stream.fail(err)
		return nil, err
	}

	select {
	case err := <-stream.openResult:
		if err != nil {
			return nil, err
		}
		return stream, nil
	case <-ctx.Done():
		stream.reset(rpcpb.GridStreamReset_CANCELLED, ctx.Err().Error())
		return nil, ctx.Err()
	}
}

func (e *Endpoint) Handle(peerAccountID int64, frame *Frame) {
	if peerAccountID <= 0 || frame.GetStreamId() == 0 {
		return
	}

	key := gridStreamKey{peerAccountID: peerAccountID, streamID: frame.GetStreamId()}
	if open := frame.GetOpen(); open != nil {
		e.handleOpen(key, open)
		return
	}

	e.mu.Lock()
	stream := e.streams[key]
	e.mu.Unlock()
	if stream == nil {
		return
	}

	switch payload := frame.Payload.(type) {
	case *rpcpb.GridStreamFrame_Accept:
		stream.handleAccept(payload.Accept)
	case *rpcpb.GridStreamFrame_Data:
		stream.handleData(payload.Data)
	case *rpcpb.GridStreamFrame_Ack:
		stream.handleAck(payload.Ack)
	case *rpcpb.GridStreamFrame_HalfClose:
		stream.handleRemoteHalfClose(payload.HalfClose.GetFinalOffset())
	case *rpcpb.GridStreamFrame_Close:
		stream.handleRemoteClose(payload.Close)
	case *rpcpb.GridStreamFrame_Reset_:
		stream.handleRemoteReset(payload.Reset_)
	default:
		stream.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "unknown GridStream frame")
	}
}

func (e *Endpoint) handleOpen(key gridStreamKey, open *rpcpb.GridStreamOpen) {
	if open.GetProtocolVersion() != gridStreamProtocolVersion ||
		open.GetService() == "" || len(open.GetService()) > gridStreamMaxServiceBytes ||
		!validGridStreamWindow(open.GetReceiveWindowBytes()) {
		e.sendReset(key, rpcpb.GridStreamReset_PROTOCOL_ERROR, "invalid GridStream open")
		return
	}

	e.mu.Lock()
	e.expireTombstonesLocked()
	if existing := e.streams[key]; existing != nil {
		e.mu.Unlock()
		existing.sendAccept()
		return
	}
	if _, recentlyClosed := e.closed[key]; recentlyClosed {
		e.mu.Unlock()
		e.sendReset(key, rpcpb.GridStreamReset_CANCELLED, "stream is already closed")
		return
	}
	handler := e.handlers[open.GetService()]
	if handler == nil {
		e.mu.Unlock()
		e.sendReset(key, rpcpb.GridStreamReset_PEER_UNAVAILABLE, "service is not available")
		return
	}
	stream := newStream(e, key.peerAccountID, key.streamID, open.GetService(), false, uint64(open.GetReceiveWindowBytes()))
	e.streams[key] = stream
	e.mu.Unlock()

	if err := stream.sendAccept(); err != nil {
		stream.fail(err)
		return
	}
	go handler(stream)
}

func (e *Endpoint) remove(stream *Stream) {
	e.mu.Lock()
	delete(e.streams, stream.key())
	e.closed[stream.key()] = time.Now().Add(gridStreamTombstoneTTL)
	e.expireTombstonesLocked()
	e.mu.Unlock()
}

func (e *Endpoint) expireTombstonesLocked() {
	now := time.Now()
	for key, expiresAt := range e.closed {
		if !expiresAt.After(now) {
			delete(e.closed, key)
		}
	}
}

func (e *Endpoint) sendReset(key gridStreamKey, reason rpcpb.GridStreamReset_Reason, message string) {
	_ = e.send(key.peerAccountID, &rpcpb.GridStreamFrame{
		StreamId: key.streamID,
		Payload: &rpcpb.GridStreamFrame_Reset_{
			Reset_: &rpcpb.GridStreamReset{Reason: reason, Message: message},
		},
	})
}

func validGridStreamWindow(window uint32) bool {
	return window > 0 && window <= gridStreamMaxWindow
}

type Stream struct {
	endpoint      *Endpoint
	peerAccountID int64
	streamID      uint32
	service       string
	initiator     bool

	mu               sync.Mutex
	cond             *sync.Cond
	sendMu           sync.Mutex
	readBuffer       bytes.Buffer
	pendingData      map[uint64][]byte
	pendingBytes     int
	receiveOffset    uint64
	remoteFinal      *uint64
	remoteClosed     bool
	remoteExitCode   int32
	remoteCloseText  string
	sendOffset       uint64
	ackedOffset      uint64
	peerWindow       uint64
	localWriteClosed bool
	terminalErr      error
	openResult       chan error
	done             chan struct{}
	doneOnce         sync.Once
	removeOnce       sync.Once
}

func newStream(endpoint *Endpoint, peerAccountID int64, streamID uint32, service string, initiator bool, peerWindow uint64) *Stream {
	stream := &Stream{
		endpoint:      endpoint,
		peerAccountID: peerAccountID,
		streamID:      streamID,
		service:       service,
		initiator:     initiator,
		pendingData:   make(map[uint64][]byte),
		peerWindow:    peerWindow,
		openResult:    make(chan error, 1),
		done:          make(chan struct{}),
	}
	stream.cond = sync.NewCond(&stream.mu)
	return stream
}

func (s *Stream) key() gridStreamKey {
	return gridStreamKey{peerAccountID: s.peerAccountID, streamID: s.streamID}
}

func (s *Stream) Service() string {
	return s.service
}

func (s *Stream) PeerAccountID() int64 {
	return s.peerAccountID
}

func (s *Stream) ID() uint32 {
	return s.streamID
}

func (s *Stream) Done() <-chan struct{} {
	return s.done
}

func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalErr
}

func (s *Stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	for s.readBuffer.Len() == 0 && s.terminalErr == nil && !s.remoteEOFLocked() {
		s.cond.Wait()
	}

	if s.readBuffer.Len() > 0 {
		n, _ := s.readBuffer.Read(p)
		ack := s.ackLocked()
		s.mu.Unlock()
		_ = s.sendFrame(ack)
		return n, nil
	}
	if s.terminalErr != nil {
		err := s.terminalErr
		s.mu.Unlock()
		return 0, err
	}
	shouldFinish := s.localWriteClosed
	s.mu.Unlock()
	if shouldFinish {
		s.finish()
	}
	return 0, io.EOF
}

func (s *Stream) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		s.mu.Lock()
		for s.terminalErr == nil && !s.localWriteClosed && s.sendOffset >= s.ackedOffset+s.peerWindow {
			s.cond.Wait()
		}
		if s.terminalErr != nil {
			err := s.terminalErr
			s.mu.Unlock()
			return written, err
		}
		if s.localWriteClosed {
			s.mu.Unlock()
			return written, errGridStreamClosed
		}
		available := int(s.ackedOffset + s.peerWindow - s.sendOffset)
		chunkSize := min(len(p), gridStreamMaxDataBytes, available)
		chunk := append([]byte(nil), p[:chunkSize]...)
		offset := s.sendOffset
		s.sendOffset += uint64(chunkSize)
		s.mu.Unlock()

		err := s.sendFrame(&rpcpb.GridStreamFrame{
			StreamId: s.streamID,
			Payload: &rpcpb.GridStreamFrame_Data{
				Data: &rpcpb.GridStreamData{Offset: offset, Payload: chunk},
			},
		})
		if err != nil {
			s.fail(err)
			return written, err
		}
		written += chunkSize
		p = p[chunkSize:]
	}
	return written, nil
}

func (s *Stream) CloseWrite() error {
	s.mu.Lock()
	if s.terminalErr != nil {
		err := s.terminalErr
		s.mu.Unlock()
		return err
	}
	if s.localWriteClosed {
		s.mu.Unlock()
		return nil
	}
	s.localWriteClosed = true
	finalOffset := s.sendOffset
	s.cond.Broadcast()
	s.mu.Unlock()

	return s.sendFrame(&rpcpb.GridStreamFrame{
		StreamId: s.streamID,
		Payload: &rpcpb.GridStreamFrame_HalfClose{
			HalfClose: &rpcpb.GridStreamHalfClose{FinalOffset: finalOffset},
		},
	})
}

func (s *Stream) CloseWithCode(exitCode int32, message string) error {
	s.mu.Lock()
	if s.terminalErr != nil {
		err := s.terminalErr
		s.mu.Unlock()
		return err
	}
	if s.localWriteClosed {
		s.mu.Unlock()
		return nil
	}
	s.localWriteClosed = true
	finalOffset := s.sendOffset
	s.cond.Broadcast()
	s.mu.Unlock()

	err := s.sendFrame(&rpcpb.GridStreamFrame{
		StreamId: s.streamID,
		Payload: &rpcpb.GridStreamFrame_Close{
			Close: &rpcpb.GridStreamClose{FinalOffset: finalOffset, ExitCode: exitCode, Message: message},
		},
	})
	s.finish()
	return err
}

func (s *Stream) Close() error {
	return s.CloseWithCode(0, "")
}

func (s *Stream) Reset(reason ResetReason, message string) {
	s.reset(rpcpb.GridStreamReset_Reason(reason), message)
}

func (s *Stream) CloseStatus() (int32, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remoteExitCode, s.remoteCloseText, s.remoteClosed
}

func (s *Stream) handleAccept(accept *rpcpb.GridStreamAccept) {
	if !s.initiator || !validGridStreamWindow(accept.GetReceiveWindowBytes()) {
		s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "invalid GridStream accept")
		return
	}
	s.mu.Lock()
	if s.peerWindow == 0 {
		s.peerWindow = uint64(accept.GetReceiveWindowBytes())
		s.cond.Broadcast()
		s.mu.Unlock()
		s.signalOpen(nil)
		return
	}
	s.mu.Unlock()
}

func (s *Stream) handleData(data *rpcpb.GridStreamData) {
	payload := data.GetPayload()
	if len(payload) == 0 || len(payload) > gridStreamMaxDataBytes {
		s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "invalid GridStream data size")
		return
	}

	s.mu.Lock()
	offset := data.GetOffset()
	end := offset + uint64(len(payload))
	if end < offset || end <= s.receiveOffset {
		ack := s.ackLocked()
		s.mu.Unlock()
		_ = s.sendFrame(ack)
		return
	}
	if offset < s.receiveOffset || end > s.receiveOffset+uint64(gridStreamDefaultWindow) {
		s.mu.Unlock()
		s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "GridStream data is outside the receive window")
		return
	}
	for pendingOffset, pending := range s.pendingData {
		pendingEnd := pendingOffset + uint64(len(pending))
		if offset == pendingOffset && bytes.Equal(payload, pending) {
			ack := s.ackLocked()
			s.mu.Unlock()
			_ = s.sendFrame(ack)
			return
		}
		if offset < pendingEnd && pendingOffset < end {
			s.mu.Unlock()
			s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "overlapping GridStream data")
			return
		}
	}
	if s.readBuffer.Len()+s.pendingBytes+len(payload) > gridStreamDefaultWindow {
		s.mu.Unlock()
		s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "GridStream receive window exceeded")
		return
	}

	s.pendingData[offset] = append([]byte(nil), payload...)
	s.pendingBytes += len(payload)
	for {
		chunk, exists := s.pendingData[s.receiveOffset]
		if !exists {
			break
		}
		delete(s.pendingData, s.receiveOffset)
		s.pendingBytes -= len(chunk)
		s.readBuffer.Write(chunk)
		s.receiveOffset += uint64(len(chunk))
	}
	ack := s.ackLocked()
	s.cond.Broadcast()
	s.mu.Unlock()
	_ = s.sendFrame(ack)
}

func (s *Stream) handleAck(ack *rpcpb.GridStreamAck) {
	s.mu.Lock()
	receivedOffset := ack.GetReceivedOffset()
	receiveWindow := uint64(ack.GetReceiveWindowBytes())
	if receivedOffset > s.sendOffset || !validGridStreamWindowOrZero(ack.GetReceiveWindowBytes()) || receivedOffset > ^uint64(0)-receiveWindow {
		s.mu.Unlock()
		s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "invalid GridStream acknowledgement")
		return
	}

	// ACK datagrams may be delivered out of order. Keep both the cumulative
	// acknowledgement and advertised window edge monotonic so a stale ACK can
	// neither reset the stream nor revoke capacity that the peer already granted.
	currentLimit := s.ackedOffset + s.peerWindow
	newLimit := receivedOffset + receiveWindow
	s.ackedOffset = max(s.ackedOffset, receivedOffset)
	s.peerWindow = max(currentLimit, newLimit) - s.ackedOffset
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *Stream) handleRemoteHalfClose(finalOffset uint64) {
	s.handleRemoteFinal(finalOffset, false, 0, "")
}

func (s *Stream) handleRemoteClose(close *rpcpb.GridStreamClose) {
	s.handleRemoteFinal(close.GetFinalOffset(), true, close.GetExitCode(), close.GetMessage())
}

func (s *Stream) handleRemoteFinal(finalOffset uint64, closed bool, exitCode int32, message string) {
	s.mu.Lock()
	if finalOffset < s.receiveOffset || finalOffset > s.receiveOffset+uint64(gridStreamDefaultWindow) {
		s.mu.Unlock()
		s.reset(rpcpb.GridStreamReset_PROTOCOL_ERROR, "invalid GridStream final offset")
		return
	}
	s.remoteFinal = &finalOffset
	if closed {
		s.remoteClosed = true
		s.remoteExitCode = exitCode
		s.remoteCloseText = message
		s.closeDone()
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *Stream) handleRemoteReset(reset *rpcpb.GridStreamReset) {
	s.fail(fmt.Errorf("grid stream reset (%s): %s", reset.GetReason(), reset.GetMessage()))
}

func (s *Stream) sendAccept() error {
	return s.sendFrame(&rpcpb.GridStreamFrame{
		StreamId: s.streamID,
		Payload: &rpcpb.GridStreamFrame_Accept{
			Accept: &rpcpb.GridStreamAccept{ReceiveWindowBytes: gridStreamDefaultWindow},
		},
	})
}

func (s *Stream) sendFrame(frame *rpcpb.GridStreamFrame) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.endpoint.send(s.peerAccountID, frame)
}

func (s *Stream) ackLocked() *rpcpb.GridStreamFrame {
	available := gridStreamDefaultWindow - s.readBuffer.Len() - s.pendingBytes
	return &rpcpb.GridStreamFrame{
		StreamId: s.streamID,
		Payload: &rpcpb.GridStreamFrame_Ack{
			Ack: &rpcpb.GridStreamAck{
				ReceivedOffset:     s.receiveOffset,
				ReceiveWindowBytes: uint32(max(available, 0)),
			},
		},
	}
}

func (s *Stream) remoteEOFLocked() bool {
	return s.remoteFinal != nil && s.receiveOffset == *s.remoteFinal && s.readBuffer.Len() == 0
}

func (s *Stream) reset(reason rpcpb.GridStreamReset_Reason, message string) {
	_ = s.sendFrame(&rpcpb.GridStreamFrame{
		StreamId: s.streamID,
		Payload: &rpcpb.GridStreamFrame_Reset_{
			Reset_: &rpcpb.GridStreamReset{Reason: reason, Message: message},
		},
	})
	s.fail(fmt.Errorf("grid stream reset (%s): %s", reason, message))
}

func (s *Stream) fail(err error) {
	s.mu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	s.signalOpen(err)
	s.closeDone()
	s.finish()
}

func (s *Stream) signalOpen(err error) {
	select {
	case s.openResult <- err:
	default:
	}
}

func (s *Stream) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *Stream) finish() {
	s.removeOnce.Do(func() { s.endpoint.remove(s) })
}

func validGridStreamWindowOrZero(window uint32) bool {
	return window <= gridStreamMaxWindow
}

func randomRequestID() uint32 {
	var value [4]byte
	if _, err := rand.Read(value[:]); err == nil {
		if id := binary.LittleEndian.Uint32(value[:]); id != 0 {
			return id
		}
	}
	return uint32(time.Now().UnixNano()) | 1
}
