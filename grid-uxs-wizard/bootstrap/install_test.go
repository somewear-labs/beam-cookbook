package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	home, bin, record string
	server            *httptest.Server
	downloads         atomic.Int32
	artifact          []byte
	checksum          string
}

func newFixture(t *testing.T, extra *tar.Header) *fixture {
	t.Helper()
	f := &fixture{home: t.TempDir()}
	f.bin = filepath.Join(f.home, "tools")
	f.record = filepath.Join(f.home, "invocation")
	if err := os.Mkdir(f.bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"id":   "#!/bin/sh\nprintf '1000\\n'\n",
		"sudo": "#!/bin/sh\n[ \"$1\" != -- ] || shift\nexec \"$@\"\n",
	} {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	files := map[string]string{
		"grid-uxs-wizard":         "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$BOOTSTRAP_RECORD\"\n[ ! -t 0 ] || printf 'tty\\n' >> \"$BOOTSTRAP_RECORD\"\n",
		"grid-uxs-wizard.json":    "{\"schemaVersion\":1,\"fileServer\":\"https://example.com/release\"}\n",
		"THIRD_PARTY_NOTICES.txt": "test\n",
		"bundle/install.json":     "{}\n", "bundle/beam.tar.gz": "beam", "bundle/tvpn.tar.gz": "tvpn",
	}
	for name, text := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(text))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		if err := archive.WriteHeader(extra); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	f.artifact = buffer.Bytes()
	f.checksum = fmt.Sprintf("%x", sha256.Sum256(f.artifact))
	source, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/install.sh":
			_, _ = w.Write(source)
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			fmt.Fprintln(w, f.checksum)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			f.downloads.Add(1)
			http.ServeContent(w, r, "bundle.tar.gz", time.Time{}, bytes.NewReader(f.artifact))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) env() []string {
	var result []string
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		switch key {
		case "HOME", "PATH", "SHELL", "XDG_CACHE_HOME", "XDG_DATA_HOME", "GRID_UXS_FILE_SERVER", "BOOTSTRAP_RECORD":
		default:
			result = append(result, value)
		}
	}
	return append(result, "HOME="+f.home, "PATH="+f.bin+":"+os.Getenv("PATH"), "SHELL=/bin/zsh", "BOOTSTRAP_RECORD="+f.record)
}

func (f *fixture) run(args ...string) ([]byte, error) {
	command := exec.Command("sh", append([]string{"install.sh", "setup", "--file-server", f.server.URL}, args...)...)
	command.Env = f.env()
	return command.CombinedOutput()
}

func TestInstallsOnUserPathAndPreservesConfiguration(t *testing.T) {
	f := newFixture(t, nil)
	for i := 0; i < 2; i++ {
		output, err := f.run("--role", "gcs", "--install-only")
		if err != nil {
			t.Fatalf("%v: %s", err, output)
		}
	}
	launcher := filepath.Join(f.home, ".local/bin/grid-uxs-wizard")
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		t.Fatal(err)
	}
	realHome, err := filepath.EvalSymlinks(f.home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resolved, filepath.Join(realHome, ".local/share/grid-uxs-wizard/releases")+string(os.PathSeparator)) {
		t.Fatal(resolved)
	}
	config, err := os.ReadFile(filepath.Join(filepath.Dir(resolved), "grid-uxs-wizard.json"))
	if err != nil || !strings.Contains(string(config), "https://example.com/release") {
		t.Fatalf("config: %s %v", config, err)
	}
	profile, err := os.ReadFile(filepath.Join(f.home, ".zprofile"))
	if err != nil || strings.Count(string(profile), `export PATH="$HOME/.local/bin:$PATH"`) != 1 {
		t.Fatalf("profile: %s %v", profile, err)
	}
	if f.downloads.Load() != 1 {
		t.Fatalf("downloaded again: %d", f.downloads.Load())
	}
	if _, err := os.Stat(f.record); !os.IsNotExist(err) {
		t.Fatal("install-only started setup")
	}
}

func TestResumesAnInterruptedArchive(t *testing.T) {
	f := newFixture(t, nil)
	arch := runtime.GOARCH
	cache := filepath.Join(f.home, ".cache/grid-uxs-wizard")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache, fmt.Sprintf("gcs-%s-%s-%s.tar.gz", runtime.GOOS, arch, f.checksum))
	if err := os.WriteFile(path, f.artifact[:len(f.artifact)/2], 0600); err != nil {
		t.Fatal(err)
	}
	output, err := f.run("--role", "gcs", "--install-only")
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, f.artifact) {
		t.Fatal("resumed archive differs", err)
	}
}

func TestRejectsTamperedDownloadsAndArchiveLinks(t *testing.T) {
	t.Run("checksum", func(t *testing.T) {
		f := newFixture(t, nil)
		f.checksum = strings.Repeat("a", 64)
		output, err := f.run("--role", "gcs", "--install-only")
		if err == nil || !strings.Contains(string(output), "checksum mismatch") {
			t.Fatalf("%v: %s", err, output)
		}
	})
	t.Run("links", func(t *testing.T) {
		f := newFixture(t, &tar.Header{Name: "bundle", Typeflag: tar.TypeSymlink, Linkname: "/tmp"})
		output, err := f.run("--role", "gcs", "--install-only")
		if err == nil || !strings.Contains(string(output), "Links are not allowed") {
			t.Fatalf("%v: %s", err, output)
		}
	})
	t.Run("traversal", func(t *testing.T) {
		f := newFixture(t, &tar.Header{Name: "../outside", Mode: 0600})
		if output, err := f.run("--role", "gcs", "--install-only"); err == nil {
			t.Fatalf("accepted traversal: %s", output)
		}
		if _, err := os.Stat(filepath.Join(f.home, ".local/share/grid-uxs-wizard/releases/outside")); !os.IsNotExist(err) {
			t.Fatal("archive escaped staging")
		}
	})
}

func TestPreservesUnmanagedCommand(t *testing.T) {
	f := newFixture(t, nil)
	launcher := filepath.Join(f.home, ".local/bin/grid-uxs-wizard")
	if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("keep me"), 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := f.run("--role", "gcs", "--install-only"); err == nil {
		t.Fatalf("overwrote command: %s", output)
	}
	data, _ := os.ReadFile(launcher)
	if string(data) != "keep me" {
		t.Fatal("existing command changed")
	}
}

func TestCurlPipeHandsTerminalToSetup(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script utility unavailable")
	}
	f := newFixture(t, nil)
	pipeline := `curl -fsSL "$1/install.sh" | sh -s -- setup --file-server "$1"`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "-c", pipeline, "bootstrap", f.server.URL)
	} else {
		quoted := strings.ReplaceAll(f.server.URL, "'", "'\\''")
		command = exec.CommandContext(ctx, "script", "-q", "-e", "-c", "sh -c '"+pipeline+"' bootstrap '"+quoted+"'", "/dev/null")
	}
	command.WaitDelay = time.Second
	command.Env = f.env()
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output := &terminalOutput{input: input}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		t.Fatalf("pipeline: %v: %s", err, output.String())
	}
	data, err := os.ReadFile(f.record)
	if err != nil || string(data) != "setup\n--role\ngcs\ntty\n" {
		t.Fatalf("setup invocation: %q %v; output: %s", data, err, output.String())
	}
}

type terminalOutput struct {
	buffer bytes.Buffer
	input  io.Writer
	once   sync.Once
}

func (o *terminalOutput) Write(data []byte) (int, error) {
	n, err := o.buffer.Write(data)
	if strings.Contains(o.String(), "Choose a number:") {
		o.once.Do(func() { _, _ = io.WriteString(o.input, "1\n") })
	}
	return n, err
}

func (o *terminalOutput) String() string { return o.buffer.String() }

func TestInstallsUxSWizardForMissionComputer(t *testing.T) {
	f := newFixture(t, nil)
	uname := "#!/bin/sh\ncase \"$1\" in -s) echo Linux ;; -m) echo aarch64 ;; *) exit 1 ;; esac\n"
	if err := os.WriteFile(filepath.Join(f.bin, "uname"), []byte(uname), 0700); err != nil {
		t.Fatal(err)
	}
	output, err := f.run("--role", "uxs", "--install-only")
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	launcher := filepath.Join(f.home, ".local/bin/grid-uxs-wizard")
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil || filepath.Base(resolved) != "grid-uxs-wizard" {
		t.Fatalf("%s: %v", resolved, err)
	}
	if _, err := os.Lstat(filepath.Join(f.home, ".local/bin/grid-gcs-wizard")); !os.IsNotExist(err) {
		t.Fatal("installed a separate GCS command")
	}
}

func TestCurlPipeRunsUpdateWithoutSudo(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script utility unavailable")
	}
	f := newFixture(t, nil)
	if err := os.WriteFile(filepath.Join(f.bin, "sudo"), []byte("#!/bin/sh\nexit 88\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pipeline := `curl -fsSL "$1/install.sh" | sh -s -- update --role gcs --file-server "$1"`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "-c", pipeline, "bootstrap", f.server.URL)
	} else {
		quoted := strings.ReplaceAll(f.server.URL, "'", "'\\''")
		command = exec.CommandContext(ctx, "script", "-q", "-e", "-c", "sh -c '"+pipeline+"' bootstrap '"+quoted+"'", "/dev/null")
	}
	command.Env = f.env()
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("update bootstrap: %v %s", err, output)
	}
	record, err := os.ReadFile(f.record)
	if err != nil || string(record) != "update\ntty\n" {
		t.Fatalf("unexpected update invocation: %v %s", err, record)
	}
}

func TestBashPathAvailableInLoginAndInteractiveShells(t *testing.T) {
	f := newFixture(t, nil)
	for repeat := 0; repeat < 2; repeat++ {
		command := exec.Command("sh", "install.sh", "--file-server", f.server.URL, "--role", "gcs", "--install-only")
		env := f.env()
		for i, value := range env {
			if strings.HasPrefix(value, "SHELL=") {
				env[i] = "SHELL=/bin/bash"
			}
		}
		command.Env = env
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, output)
		}
	}
	for _, name := range []string{".profile", ".bashrc"} {
		body, err := os.ReadFile(filepath.Join(f.home, name))
		if err != nil || strings.Count(string(body), `export PATH="$HOME/.local/bin:$PATH"`) != 1 {
			t.Fatalf("%s: %v", name, err)
		}
		command := exec.Command("sh", "-c", `. "$HOME/`+name+`"; command -v grid-uxs-wizard`)
		command.Env = f.env()
		output, err := command.CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != filepath.Join(f.home, ".local/bin/grid-uxs-wizard") {
			t.Fatalf("PATH did not select user wizard: %s %v", output, err)
		}
	}
}
