package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type sitlOptions struct {
	Binary, SHA256, Endpoint, Vehicle, Home, State, Defaults string
	SystemID, Instance                                       int
}

func executeSITL(args []string) error {
	f := flag.NewFlagSet("sitl", flag.ContinueOnError)
	var o sitlOptions
	f.StringVar(&o.Binary, "binary", "", "Trusted target-architecture ArduPilot binary")
	f.StringVar(&o.SHA256, "sha256", "", "Expected SHA256 of the supplied binary")
	f.StringVar(&o.Endpoint, "mavlink-endpoint", "", "Existing local MAVLink receiver, IPv4:PORT")
	f.StringVar(&o.Vehicle, "vehicle", "copter", "Simulator: copter, rover or boat")
	f.StringVar(&o.Home, "home", "42.34120,-71.03530,10,0", "LAT,LON,ALT,YAW")
	f.StringVar(&o.State, "state", "./uxs-sitl", "Persistent simulator state directory")
	f.StringVar(&o.Defaults, "defaults", "", "Optional matching parameter defaults")
	f.IntVar(&o.SystemID, "sysid", 1, "MAVLink system ID, 1..255")
	f.IntVar(&o.Instance, "instance", 0, "Auxiliary port offset, 0..100")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if runtime.GOOS != "linux" {
		return errors.New("run SITL on the Linux mission computer")
	}
	if err := o.validate(); err != nil {
		return err
	}
	if err := checkArchitecture(o.Binary, runtime.GOARCH); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return runSITL(ctx, o)
}
func (o sitlOptions) model() string {
	return map[string]string{"copter": "quad", "rover": "rover", "boat": "motorboat"}[o.Vehicle]
}
func (o sitlOptions) validate() error {
	if o.Binary == "" || o.State == "" || o.model() == "" || o.SystemID < 1 || o.SystemID > 255 || o.Instance < 0 || o.Instance > 100 {
		return errors.New("provide a binary/state, valid vehicle, sysid 1..255 and instance 0..100")
	}
	hash, err := hex.DecodeString(o.SHA256)
	if err != nil || len(hash) != 32 {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	host, port, err := net.SplitHostPort(o.Endpoint)
	if err != nil {
		return errors.New("MAVLink endpoint must be IPv4:PORT")
	}
	address, err := netip.ParseAddr(host)
	if err != nil || !address.Is4() {
		return errors.New("MAVLink endpoint must use numeric IPv4")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("MAVLink port must be 1..65535")
	}
	parts := strings.Split(o.Home, ",")
	if len(parts) != 4 {
		return errors.New("home must be LAT,LON,ALT,YAW")
	}
	var values [4]float64
	for index, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("home coordinates must be finite numbers")
		}
		values[index] = value
	}
	if values[0] < -90 || values[0] > 90 || values[1] < -180 || values[1] > 180 || values[3] < 0 || values[3] >= 360 {
		return errors.New("invalid home latitude, longitude or yaw")
	}
	return nil
}
func stageSITL(o sitlOptions) (string, error) {
	actual, err := fileDigest(o.Binary)
	if err != nil {
		return "", err
	}
	expected := strings.ToLower(o.SHA256)
	if actual != expected {
		return "", errors.New("SITL binary checksum mismatch")
	}
	defaultsDigest := "none"
	if o.Defaults != "" {
		defaultsDigest, err = fileDigest(o.Defaults)
		if err != nil {
			return "", err
		}
	}
	config := fmt.Sprintf("sha256=%s\nmodel=%s\nsysid=%d\ninstance=%d\nhome=%s\nendpoint=%s\ndefaultsSha256=%s\n", expected, o.model(), o.SystemID, o.Instance, o.Home, o.Endpoint, defaultsDigest)
	path := filepath.Join(o.State, "config")
	saved, err := os.ReadFile(path)
	if err == nil {
		if string(saved) != config {
			return "", errors.New("saved SITL configuration differs; use a new state directory")
		}
		for name, digest := range map[string]string{"ardupilot": expected, "defaults.parm": defaultsDigest} {
			if digest == "none" {
				continue
			}
			actual, err := fileDigest(filepath.Join(o.State, name))
			if err != nil || actual != digest {
				return "", fmt.Errorf("installed %s is missing or modified", name)
			}
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := copyVerified(o.Binary, filepath.Join(o.State, "ardupilot"), expected); err != nil {
			return "", err
		}
		if o.Defaults != "" {
			if err := copyVerified(o.Defaults, filepath.Join(o.State, "defaults.parm"), defaultsDigest); err != nil {
				return "", err
			}
		}
		if err := atomicPrivateWrite(path, []byte(config)); err != nil {
			return "", err
		}
	}
	return filepath.Join(o.State, "ardupilot"), nil
}
func runSITL(ctx context.Context, o sitlOptions) error {
	state, err := filepath.Abs(o.State)
	if err != nil {
		return err
	}
	o.State = state
	if info, err := os.Lstat(state); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("SITL state must be a real directory")
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(state, "lock")); err == nil {
		return errors.New("legacy SITL lock exists; stop its owner before using the Go runner")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	release, err := lockSITL(state)
	if err != nil {
		return err
	}
	defer release()
	binary, err := stageSITL(o)
	if err != nil {
		return err
	}
	args := []string{"--model", o.model(), "--home", o.Home, "--sysid", strconv.Itoa(o.SystemID), "--instance", strconv.Itoa(o.Instance), "--serial0", "udpclient:" + o.Endpoint}
	if o.Defaults != "" {
		args = append(args, "--defaults", filepath.Join(state, "defaults.parm"))
	}
	log, err := os.OpenFile(filepath.Join(state, "sitl.log"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	_, _ = fmt.Fprintf(log, "\nLaunch %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(os.Stdout, "Starting %s SITL, system ID %d, MAVLink destination %s\nLog: %s/sitl.log\n", o.Vehicle, o.SystemID, o.Endpoint, state)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = state
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("SITL exited; inspect %s/sitl.log: %w", state, err)
	}
	return nil
}
