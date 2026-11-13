package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSoftwareRequiresMatchingPlatformAndPackageFormat(t *testing.T) {
	p := softwarePackage{Source: "beam.msi", SHA256: strings.Repeat("a", 64), Format: "msi", Command: "/installed/beam"}
	if err := p.validate("linux"); err == nil {
		t.Fatal("accepted Windows installer on Linux")
	}
	p.Format = "tar.gz"
	p.Destination = "/opt/somewear/beam/version"
	p.Command = "../../beam"
	if err := p.validate("linux"); err == nil {
		t.Fatal("accepted escaping command")
	}
	p.Command = "bin/beam"
	if err := p.validate("linux"); err != nil {
		t.Fatal(err)
	}
}
func TestSoftwareChecksDigestBeforeInstalling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package")
	_ = os.WriteFile(path, []byte("software"), 0600)
	p := softwarePackage{Source: "package", SHA256: strings.Repeat("0", 64)}
	if _, err := prepareSoftware(context.Background(), dir, t.TempDir(), p); err == nil {
		t.Fatal("accepted changed package")
	}
	p.SHA256, _ = fileDigest(path)
	if _, err := prepareSoftware(context.Background(), dir, t.TempDir(), p); err != nil {
		t.Fatal(err)
	}
	p.Source = "../outside"
	if _, err := prepareSoftware(context.Background(), dir, t.TempDir(), p); err == nil {
		t.Fatal("accepted package outside bundle")
	}
}
func TestNativePackageCommandsPreservePathsAsArguments(t *testing.T) {
	for _, item := range []struct{ format, command string }{{"msi", "msiexec.exe"}, {"pkg", "/usr/sbin/installer"}, {"deb", "dpkg"}, {"rpm", "rpm"}} {
		p := preparedPackage{softwarePackage: softwarePackage{Format: item.format}, Path: "/a path/package"}
		command, args, err := packageCommand("", p)
		if err != nil || command != item.command {
			t.Fatal(command, args, err)
		}
		found := false
		for _, arg := range args {
			if arg == p.Path {
				found = true
			}
		}
		if !found {
			t.Fatal("package path split or lost")
		}
	}
}

func TestArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "../../outside", Mode: 0755, Size: 1, Typeflag: tar.TypeReg},
		{Name: "bin/link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink},
	} {
		archive := writeTestArchive(t, header, []byte("x"))
		if err := unpackSoftwareArchive(archive, t.TempDir(), "bin/beam"); err == nil {
			t.Fatal("accepted unsafe archive")
		}
	}
}
func TestArchiveInstallsExecutableWithoutShellAndStripsSpecialModes(t *testing.T) {
	archive := writeTestArchive(t, &tar.Header{Name: "bin/beam", Mode: 04755, Size: 4, Typeflag: tar.TypeReg}, []byte("beam"))
	stage := t.TempDir()
	if err := unpackSoftwareArchive(archive, stage, "bin/beam"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(stage, "bin/beam"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetuid != 0 || info.Mode().Perm()&0111 == 0 {
		t.Fatal("unsafe or non-executable installed mode")
	}
}
func writeTestArchive(t *testing.T, header *tar.Header, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(f)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if header.Typeflag == tar.TypeReg {
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBothRolesVerifyBothPackagesBeforeInstallingEither(t *testing.T) {
	for _, role := range []string{"gcs", "uxs"} {
		dir := t.TempDir()
		beam := writeTestArchive(t, &tar.Header{Name: "bin/beam", Mode: 0755, Size: 4, Typeflag: tar.TypeReg}, []byte("beam"))
		beamData, _ := os.ReadFile(beam)
		_ = os.WriteFile(filepath.Join(dir, "beam.tar.gz"), beamData, 0600)
		digest, _ := fileDigest(filepath.Join(dir, "beam.tar.gz"))
		_ = os.WriteFile(filepath.Join(dir, "tvpn.tar.gz"), []byte("corrupt"), 0600)
		bundle := softwareBundle{SchemaVersion: 1, OS: runtime.GOOS, Arch: runtime.GOARCH,
			Beam: softwarePackage{Source: "beam.tar.gz", SHA256: digest, Format: "tar.gz", Destination: "/opt/somewear/beam/v1", Command: "bin/beam"},
			TVPN: softwarePackage{Source: "tvpn.tar.gz", SHA256: strings.Repeat("0", 64), Format: "tar.gz", Destination: "/opt/somewear/tvpn/v1", Command: "tvpn"}}
		data, _ := json.Marshal(bundle)
		_ = os.WriteFile(filepath.Join(dir, "install.json"), data, 0600)
		called := false
		run := func(context.Context, string, ...string) error { called = true; return nil }
		var err error
		if role == "gcs" {
			i := gcsInstaller{opts: gcsOptions{Bundle: dir}, run: run, emit: func(string) {}}
			err = i.install(context.Background())
		} else {
			i := installer{opts: options{Bundle: dir, State: "/var/lib/uxs"}, run: run, emit: func(string) {}}
			err = i.installTargetPackages(context.Background())
		}
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || called {
			t.Fatalf("%s installed before checking TVPN: %v", role, err)
		}
	}
}

func TestMissionComputerArchiveAcceptsNonExecutableJar(t *testing.T) {
	archive := writeTestArchive(t, &tar.Header{Name: "beam.jar", Mode: 0644, Size: 3, Typeflag: tar.TypeReg}, []byte("jar"))
	if err := unpackSoftwareArchive(archive, t.TempDir(), "beam.jar"); err != nil {
		t.Fatal(err)
	}
}
