package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type managedProcess interface {
	Exited() bool
	Stop()
}
type processStarter func(string, []string, map[string]string, string) (managedProcess, error)
type runtimeProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func runtimeEnvironment(overrides map[string]string) []string {
	result := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[key]; !ok {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func startRuntimeProcess(command string, args []string, environment map[string]string, logPath string) (managedProcess, error) {
	if err := checkInstallParents(logPath); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(logPath); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("process log is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(command, args...)
	cmd.Env = runtimeEnvironment(environment)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	process := &runtimeProcess{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); _ = log.Close(); close(process.done) }()
	return process, nil
}
func (p *runtimeProcess) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
func (p *runtimeProcess) Stop() {
	if p.Exited() {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return
	case <-timer.C:
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}
