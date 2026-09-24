// Package planbundle packs a run's saved plan for the trip between its plan
// phase and its apply phase, which usually run on different executors.
//
// A bundle is the plan file plus the dependency lock file, so the apply phase
// installs the provider versions the plan was made with: tofu refuses a saved
// plan whose providers changed. It is encrypted before it leaves the executor,
// because a plan file holds variable values and sensitive attributes in
// plaintext, and the key it is encrypted with belongs to the run and never
// crosses Temporal in the clear.
package planbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/vishu42/tflive/internal/runner"
)

// LockFileName is tofu's dependency lock file in the root module directory.
const LockFileName = ".terraform.lock.hcl"

// KeySize is the length of a plan key: AES-256.
const KeySize = 32

// maxFileSize bounds each file unpacked from a bundle. Plan files for large
// configurations run to tens of megabytes; this is well past that and still
// small enough that a hostile bundle cannot fill the executor's disk.
const maxFileSize = 512 << 20

// bundled is every file a bundle may carry. Unpack refuses anything else.
var bundled = []string{runner.PlanFileName, LockFileName}

// NewKey returns a fresh random plan key.
func NewKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate plan key: %w", err)
	}
	return key, nil
}

// Pack reads the saved plan, and the lock file when there is one, from dir.
// The plan file must exist; a module with no providers has no lock file.
func Pack(dir string) ([]byte, error) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	for _, name := range bundled {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) && name == LockFileName {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
			return nil, fmt.Errorf("pack %s: %w", name, err)
		}
		if _, err := archive.Write(content); err != nil {
			return nil, fmt.Errorf("pack %s: %w", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("close plan bundle: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("compress plan bundle: %w", err)
	}
	return buffer.Bytes(), nil
}

// Unpack writes a bundle's files into dir, replacing any already there: the
// lock file in the fetched source, if it has one, gives way to the one the
// plan was made with.
func Unpack(bundle []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return fmt.Errorf("open plan bundle: %w", err)
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	sawPlan := false
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read plan bundle: %w", err)
		}
		if !isBundled(header.Name) || header.Typeflag != tar.TypeReg {
			return fmt.Errorf("plan bundle holds unexpected entry %q", header.Name)
		}
		if header.Size > maxFileSize {
			return fmt.Errorf("plan bundle entry %q is too large", header.Name)
		}
		content, err := io.ReadAll(io.LimitReader(archive, maxFileSize))
		if err != nil {
			return fmt.Errorf("read %s from plan bundle: %w", header.Name, err)
		}
		// isBundled admitted header.Name above, so it cannot traverse out of dir.
		if err := os.WriteFile(filepath.Join(dir, header.Name), content, 0o600); err != nil { //nolint:gosec // see above
			return fmt.Errorf("write %s: %w", header.Name, err)
		}
		sawPlan = sawPlan || header.Name == runner.PlanFileName
	}
	if !sawPlan {
		return errors.New("plan bundle holds no plan file")
	}
	return nil
}

func isBundled(name string) bool {
	return slices.Contains(bundled, name)
}

// Seal encrypts a bundle with key. context is bound in as additional data, so
// a bundle only opens for the run it was sealed for, even under the right key.
// The nonce is random and prepended.
func Seal(key []byte, bundle []byte, context string) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate plan nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, bundle, []byte(context)), nil
}

// Open decrypts a sealed bundle, failing if it was altered, sealed under
// another key, or sealed for another context.
func Open(key []byte, sealed []byte, context string) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("sealed plan is too short")
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	bundle, err := aead.Open(nil, nonce, ciphertext, []byte(context))
	if err != nil {
		return nil, fmt.Errorf("open sealed plan: %w", err)
	}
	return bundle, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("plan key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("plan cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("plan cipher: %w", err)
	}
	return aead, nil
}
