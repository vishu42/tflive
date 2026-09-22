package planbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleRoundTripsThePlanAndLockFile(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	writeFile(t, source, "tfplan", "plan bytes")
	writeFile(t, source, ".terraform.lock.hcl", "locked providers")
	writeFile(t, source, "main.tf", "not bundled")

	bundle, err := Pack(source)
	if err != nil {
		t.Fatalf("Pack returned error: %v", err)
	}
	key, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey returned error: %v", err)
	}
	sealed, err := Seal(key, bundle, "tenant_123/run_123")
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if bytes.Contains(sealed, []byte("plan bytes")) {
		t.Fatal("sealed bundle contains the plan in plaintext")
	}

	opened, err := Open(key, sealed, "tenant_123/run_123")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	target := t.TempDir()
	// The fetched source's own lock file gives way to the plan's.
	writeFile(t, target, ".terraform.lock.hcl", "newer providers")
	if err := Unpack(opened, target); err != nil {
		t.Fatalf("Unpack returned error: %v", err)
	}
	assertFile(t, target, "tfplan", "plan bytes")
	assertFile(t, target, ".terraform.lock.hcl", "locked providers")
	if _, err := os.Stat(filepath.Join(target, "main.tf")); !os.IsNotExist(err) {
		t.Fatalf("main.tf was unpacked: %v", err)
	}
}

// A module with no providers has no lock file; its plan still bundles.
func TestPackWithoutALockFile(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	writeFile(t, source, "tfplan", "plan bytes")

	bundle, err := Pack(source)
	if err != nil {
		t.Fatalf("Pack returned error: %v", err)
	}
	target := t.TempDir()
	if err := Unpack(bundle, target); err != nil {
		t.Fatalf("Unpack returned error: %v", err)
	}
	assertFile(t, target, "tfplan", "plan bytes")
}

func TestPackRequiresThePlan(t *testing.T) {
	t.Parallel()

	if _, err := Pack(t.TempDir()); err == nil {
		t.Fatal("Pack returned nil error without a plan file")
	}
}

// A bundle only opens for the run it was sealed for, and only unaltered.
func TestOpenRejectsAnotherRunTamperingAndTheWrongKey(t *testing.T) {
	t.Parallel()

	key, _ := NewKey()
	other, _ := NewKey()
	sealed, err := Seal(key, []byte("bundle"), "tenant_123/run_123")
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}

	if _, err := Open(key, sealed, "tenant_123/run_456"); err == nil {
		t.Fatal("Open accepted a bundle sealed for another run")
	}
	if _, err := Open(other, sealed, "tenant_123/run_123"); err == nil {
		t.Fatal("Open accepted the wrong key")
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Open(key, tampered, "tenant_123/run_123"); err == nil {
		t.Fatal("Open accepted a tampered bundle")
	}
}

// Unpack writes only the two files a bundle may carry, so a bundle cannot be
// used to drop anything else, anywhere else, on the executor.
func TestUnpackRefusesAnythingButThePlanAndLockFile(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"main.tf", "../escape", "/etc/passwd"} {
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		archive := tar.NewWriter(gz)
		_ = archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: 1})
		_, _ = archive.Write([]byte("x"))
		_ = archive.Close()
		_ = gz.Close()

		if err := Unpack(buffer.Bytes(), t.TempDir()); err == nil {
			t.Fatalf("Unpack accepted entry %q", name)
		}
	}
}

func writeFile(t *testing.T, dir string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, dir string, name string, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
