package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	rpcpb "somewear/rpc/proto"
	"somewear/rpc/stream"
)

func (s *rpcServer) handleStreamExec(reqID uint32, targetUserID int64, route sessionRoute, req *rpcpb.ExecRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	output, err := stream.OpenCommandOutput(ctx, s.streams, targetUserID, stream.Route{
		SessionID: route.id,
		Channels:  route.channels,
	})
	cancel()
	if err != nil {
		s.sendError(reqID, targetUserID, route, fmt.Sprintf("open command output: %v", err))
		return
	}

	s.cwdMu.Lock()
	cwd := s.cwd
	s.cwdMu.Unlock()

	cwdReader, cwdWriter, err := os.Pipe()
	if err != nil {
		_ = output.CloseWithCode(1, err.Error())
		return
	}
	defer cwdReader.Close()
	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		cwdWriter.Close()
		_ = output.CloseWithCode(1, err.Error())
		return
	}
	defer outputReader.Close()

	const script = `{ eval "$1"; }; __ec=$?; pwd >&3; exit $__ec`
	cmd := exec.Command("sh", "-c", script, "grid-remote-shell", req.GetCommand())
	cmd.Dir = cwd
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	cmd.ExtraFiles = []*os.File{cwdWriter}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cwdWriter.Close()
		outputWriter.Close()
		_ = output.CloseWithCode(1, err.Error())
		return
	}
	cwdWriter.Close()
	outputWriter.Close()

	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(output, outputReader)
		copyDone <- copyErr
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-output.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		select {
		case waitErr = <-waitDone:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			waitErr = <-waitDone
		}
	}
	copyErr := <-copyDone

	newCwd, _ := io.ReadAll(cwdReader)
	if resolved := strings.TrimSpace(string(newCwd)); resolved != "" {
		s.cwdMu.Lock()
		s.cwd = resolved
		s.cwdMu.Unlock()
	}

	if copyErr != nil {
		output.Reset(stream.ResetInternalError, copyErr.Error())
		return
	}
	exitCode := int32(0)
	message := ""
	if waitErr != nil {
		message = waitErr.Error()
		if exitError, ok := waitErr.(*exec.ExitError); ok {
			exitCode = int32(exitError.ExitCode())
		} else {
			exitCode = 1
		}
	}
	_ = output.CloseWithCode(exitCode, message)
}
