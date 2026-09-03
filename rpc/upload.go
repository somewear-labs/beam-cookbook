package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	rpcpb "somewear/rpc/proto"
)

const (
	rpcUploadChunkSize = 32 * 1024
	rpcUploadMaxSize   = 8 * 1024 * 1024
	rpcUploadMaxName   = 255
	rpcUploadTTL       = 10 * time.Minute
)

type uploadKey struct {
	peerAccountID int64
	transferID    string
}

type uploadState struct {
	file        *os.File
	temporary   string
	destination string
	name        string
	mode        os.FileMode
	totalSize   uint64
	nextOffset  uint64
	complete    bool
	updatedAt   time.Time
}

type fileSender func(string, int, int64, string, []rpcpb.SessionChannel) error

func uploadFileDirectRPC(
	beamURL string,
	workspace int,
	targetUserID int64,
	route sessionRoute,
	localPath string,
	timeout time.Duration,
	nextID, pendingID *atomic.Uint32,
	responses <-chan inboundEnvelope,
	send packageSender,
) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path must name a regular file")
	}
	if info.Size() > rpcUploadMaxSize {
		return "", fmt.Errorf("file exceeds %d-byte upload limit", rpcUploadMaxSize)
	}
	name := filepath.Base(localPath)
	if !validUploadName(name) {
		return "", errors.New("file name is invalid")
	}

	transferID := fmt.Sprintf("%x-%x", time.Now().UnixNano(), randomRequestID())
	buffer := make([]byte, rpcUploadChunkSize)
	var offset uint64
	for {
		n, readErr := io.ReadFull(file, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return "", readErr
		}
		complete := offset+uint64(n) == uint64(info.Size())
		request := &rpcpb.PutRequest{
			TransferId: transferID,
			Name:       name,
			Mode:       uint32(info.Mode().Perm()),
			Offset:     offset,
			TotalSize:  uint64(info.Size()),
			Data:       append([]byte(nil), buffer[:n]...),
			Complete:   complete,
		}

		response, err := sendPutChunkDirect(
			beamURL, workspace, targetUserID, route, request, timeout,
			nextID, pendingID, responses, send,
		)
		if err != nil {
			return "", err
		}
		expectedOffset := offset + uint64(n)
		if response.GetNextOffset() != expectedOffset {
			return "", fmt.Errorf("target acknowledged offset %d, want %d", response.GetNextOffset(), expectedOffset)
		}
		offset = response.GetNextOffset()
		if complete {
			if !response.GetComplete() || response.GetPath() == "" {
				return "", errors.New("target did not complete the upload")
			}
			return response.GetPath(), nil
		}
	}
}

func sendPutChunkDirect(
	beamURL string,
	workspace int,
	targetUserID int64,
	route sessionRoute,
	request *rpcpb.PutRequest,
	timeout time.Duration,
	nextID, pendingID *atomic.Uint32,
	responses <-chan inboundEnvelope,
	send packageSender,
) (*rpcpb.PutResponse, error) {
	id := nextID.Add(1)
	pendingID.Store(id)
	defer pendingID.Store(0)

	envelope := &rpcpb.Envelope{
		RequestId: id,
		SessionId: route.id,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Put{Put: request},
		}},
	}
	payload, err := marshalEnvelope(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode upload chunk: %w", err)
	}
	if err := send(beamURL, workspace, targetUserID, payload, route.channels); err != nil {
		return nil, fmt.Errorf("send upload chunk: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case inbound := <-responses:
			if inbound.envelope.GetRequestId() != id || inbound.envelope.GetSessionId() != route.id {
				continue
			}
			response := inbound.envelope.GetResponse()
			if put := response.GetPut(); put != nil {
				return put, nil
			}
			if rpcError := response.GetError(); rpcError != nil {
				return nil, errors.New(rpcError.GetMessage())
			}
		case <-timer.C:
			return nil, fmt.Errorf("upload chunk timed out after %s", timeout)
		}
	}
}

func uploadFileRPC(
	beamURL string,
	workspace int,
	targetUserID int64,
	route sessionRoute,
	localPath string,
	timeout time.Duration,
	nextID, pendingID *atomic.Uint32,
	responses <-chan inboundEnvelope,
	send fileSender,
) (string, error) {
	contents, err := os.ReadFile(localPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path must name a regular file")
	}
	if info.Size() > rpcUploadMaxSize {
		return "", fmt.Errorf("file exceeds %d-byte upload limit", rpcUploadMaxSize)
	}
	name := filepath.Base(localPath)
	if !validUploadName(name) {
		return "", errors.New("file name is invalid")
	}

	request := &rpcpb.PutRequest{
		TransferId: fmt.Sprintf("%x-%x", time.Now().UnixNano(), randomRequestID()),
		Name:       name,
		Mode:       uint32(info.Mode().Perm()),
		TotalSize:  uint64(info.Size()),
		Data:       contents,
		Complete:   true,
	}
	response, err := sendPutFile(beamURL, workspace, targetUserID, route, request, timeout, nextID, pendingID, responses, send)
	if err != nil {
		return "", err
	}
	if response.GetNextOffset() != uint64(info.Size()) {
		return "", fmt.Errorf("target acknowledged offset %d, want %d", response.GetNextOffset(), info.Size())
	}
	if !response.GetComplete() || response.GetPath() == "" {
		return "", errors.New("target did not complete the upload")
	}
	return response.GetPath(), nil
}

func sendPutFile(
	beamURL string,
	workspace int,
	targetUserID int64,
	route sessionRoute,
	request *rpcpb.PutRequest,
	timeout time.Duration,
	nextID, pendingID *atomic.Uint32,
	responses <-chan inboundEnvelope,
	send fileSender,
) (*rpcpb.PutResponse, error) {
	id := nextID.Add(1)
	pendingID.Store(id)
	defer pendingID.Store(0)

	envelope := &rpcpb.Envelope{
		RequestId: id,
		SessionId: route.id,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Put{Put: request},
		}},
	}
	payload, err := marshalEnvelopeBytes(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode upload: %w", err)
	}
	tempFile, err := os.CreateTemp("", "grid-put-*.rpc")
	if err != nil {
		return nil, fmt.Errorf("create upload envelope: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.Write(payload); err != nil {
		tempFile.Close()
		return nil, fmt.Errorf("write upload envelope: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return nil, fmt.Errorf("close upload envelope: %w", err)
	}
	if err := send(beamURL, workspace, targetUserID, tempPath, route.channels); err != nil {
		return nil, fmt.Errorf("send upload: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case inbound := <-responses:
			if inbound.envelope.GetRequestId() != id {
				continue
			}
			if inbound.envelope.GetSessionId() != route.id {
				continue
			}
			response := inbound.envelope.GetResponse()
			if put := response.GetPut(); put != nil {
				return put, nil
			}
			if rpcError := response.GetError(); rpcError != nil {
				return nil, errors.New(rpcError.GetMessage())
			}
		case <-timer.C:
			return nil, fmt.Errorf("upload timed out after %s", timeout)
		}
	}
}

func (s *rpcServer) handlePut(reqID uint32, sourceUserID int64, route sessionRoute, request *rpcpb.PutRequest) {
	response, err := s.applyPutChunk(sourceUserID, request)
	if err != nil {
		s.sendError(reqID, sourceUserID, route, err.Error())
		return
	}
	envelope := &rpcpb.Envelope{
		RequestId: reqID,
		SessionId: route.id,
		Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
			Result: &rpcpb.RpcResponse_Put{Put: response},
		}},
	}
	payload, err := marshalEnvelope(envelope)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to marshal upload response:", err)
		return
	}
	if err := sendIPv4WithChannels(s.beamURL, s.workspaceID, sourceUserID, payload, route.channels); err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to send upload response:", err)
	}
}

func (s *rpcServer) applyPutChunk(sourceUserID int64, request *rpcpb.PutRequest) (*rpcpb.PutResponse, error) {
	if request == nil || request.GetTransferId() == "" || len(request.GetTransferId()) > 64 {
		return nil, errors.New("invalid upload transfer ID")
	}
	if !validUploadName(request.GetName()) {
		return nil, errors.New("invalid upload file name")
	}
	if request.GetTotalSize() > rpcUploadMaxSize {
		return nil, fmt.Errorf("file exceeds %d-byte upload limit", rpcUploadMaxSize)
	}
	if len(request.GetData()) > rpcUploadMaxSize {
		return nil, fmt.Errorf("upload data exceeds %d bytes", rpcUploadMaxSize)
	}

	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	s.expireUploadsLocked(time.Now())
	if s.uploads == nil {
		s.uploads = make(map[uploadKey]*uploadState)
	}

	key := uploadKey{peerAccountID: sourceUserID, transferID: request.GetTransferId()}
	state := s.uploads[key]
	if state == nil {
		if request.GetOffset() != 0 {
			return nil, errors.New("upload transfer has not started")
		}
		created, err := s.createUploadLocked(sourceUserID, request)
		if err != nil {
			return nil, err
		}
		state = created
		s.uploads[key] = state
	}
	if state.name != request.GetName() || state.totalSize != request.GetTotalSize() || state.mode != normalizedUploadMode(request.GetMode()) {
		return nil, errors.New("upload metadata changed during transfer")
	}
	state.updatedAt = time.Now()

	if request.GetOffset() < state.nextOffset {
		return &rpcpb.PutResponse{NextOffset: state.nextOffset, Path: state.destination, Complete: state.complete}, nil
	}
	if request.GetOffset() != state.nextOffset {
		return nil, fmt.Errorf("unexpected upload offset %d; want %d", request.GetOffset(), state.nextOffset)
	}
	if state.complete {
		return &rpcpb.PutResponse{NextOffset: state.nextOffset, Path: state.destination, Complete: true}, nil
	}
	if state.nextOffset+uint64(len(request.GetData())) > state.totalSize {
		return nil, errors.New("upload exceeds declared size")
	}
	if _, err := state.file.Write(request.GetData()); err != nil {
		s.discardUploadLocked(key, state)
		return nil, err
	}
	state.nextOffset += uint64(len(request.GetData()))
	if !request.GetComplete() {
		return &rpcpb.PutResponse{NextOffset: state.nextOffset}, nil
	}
	if state.nextOffset != state.totalSize {
		return nil, fmt.Errorf("upload size mismatch: received %d of %d bytes", state.nextOffset, state.totalSize)
	}
	if err := state.file.Chmod(state.mode); err != nil {
		s.discardUploadLocked(key, state)
		return nil, err
	}
	if err := state.file.Close(); err != nil {
		s.discardUploadLocked(key, state)
		return nil, err
	}
	state.file = nil
	if err := os.Rename(state.temporary, state.destination); err != nil {
		s.discardUploadLocked(key, state)
		return nil, err
	}
	state.temporary = ""
	state.complete = true
	return &rpcpb.PutResponse{NextOffset: state.nextOffset, Path: state.destination, Complete: true}, nil
}

func (s *rpcServer) createUploadLocked(sourceUserID int64, request *rpcpb.PutRequest) (*uploadState, error) {
	root := s.uploadRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "grid-remote-shell-uploads")
	}
	directory := filepath.Join(root, strconv.FormatInt(sourceUserID, 10))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return nil, err
	}
	return &uploadState{
		file:        file,
		temporary:   file.Name(),
		destination: filepath.Join(directory, request.GetName()),
		name:        request.GetName(),
		mode:        normalizedUploadMode(request.GetMode()),
		totalSize:   request.GetTotalSize(),
		updatedAt:   time.Now(),
	}, nil
}

func (s *rpcServer) expireUploadsLocked(now time.Time) {
	for key, state := range s.uploads {
		if now.Sub(state.updatedAt) > rpcUploadTTL {
			s.discardUploadLocked(key, state)
		}
	}
}

func (s *rpcServer) discardUploadLocked(key uploadKey, state *uploadState) {
	if state.file != nil {
		_ = state.file.Close()
	}
	if state.temporary != "" {
		_ = os.Remove(state.temporary)
	}
	delete(s.uploads, key)
}

func normalizedUploadMode(mode uint32) os.FileMode {
	permissions := os.FileMode(mode) & os.ModePerm
	if permissions == 0 {
		return 0o600
	}
	return permissions
}

func validUploadName(name string) bool {
	return name != "" && len(name) <= rpcUploadMaxName && filepath.Base(name) == name && name != "." && name != string(filepath.Separator)
}
