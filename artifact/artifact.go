package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const copyBufferSize = 1024 * 64

// Artifact describes an immutable file by content hash and source metadata.
type Artifact struct {
	Path    string
	Name    string
	SHA256  string
	Size    int64
	ModTime time.Time
}

// Stored is the result of putting an artifact into a Store.
type Stored struct {
	Artifact
	StorePath     string
	AlreadyExists bool
}

// Store stages files by SHA-256 under Root.
type Store struct {
	Root string
}

// Inspect hashes path and returns source metadata without copying the file.
func Inspect(path string) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Artifact{}, err
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("inspect artifact: %s is not a regular file", path)
	}

	sha, size, err := Digest(f)
	if err != nil {
		return Artifact{}, err
	}

	return Artifact{
		Path:    path,
		Name:    filepath.Base(path),
		SHA256:  sha,
		Size:    size,
		ModTime: info.ModTime(),
	}, nil
}

// Digest reads r and returns its SHA-256 hex digest and byte count.
func Digest(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Path validates sha and returns its content-addressed path.
func (s Store) Path(sha string) (string, error) {
	if len(sha) != sha256.Size*2 {
		return "", fmt.Errorf("artifact digest must be %d hexadecimal characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return "", fmt.Errorf("artifact digest must be hexadecimal: %w", err)
	}
	return filepath.Join(s.Root, sha[:2], sha), nil
}

// Put copies path into the store unless an artifact with the same SHA-256 exists.
func (s Store) Put(ctx context.Context, path string) (Stored, error) {
	if s.Root == "" {
		return Stored{}, errors.New("artifact store root is required")
	}

	src, err := os.Open(path)
	if err != nil {
		return Stored{}, err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return Stored{}, err
	}
	if !info.Mode().IsRegular() {
		return Stored{}, fmt.Errorf("put artifact: %s is not a regular file", path)
	}

	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return Stored{}, err
	}

	tmp, err := os.CreateTemp(s.Root, ".tmp-*")
	if err != nil {
		return Stored{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	sha, size, err := copyAndDigest(ctx, tmp, src)
	if err != nil {
		_ = tmp.Close()
		return Stored{}, err
	}
	if err := tmp.Close(); err != nil {
		return Stored{}, err
	}

	a := Artifact{
		Path:    path,
		Name:    filepath.Base(path),
		SHA256:  sha,
		Size:    size,
		ModTime: info.ModTime(),
	}
	storePath, err := s.Path(a.SHA256)
	if err != nil {
		return Stored{}, err
	}
	if _, err := os.Stat(storePath); err == nil {
		return Stored{Artifact: a, StorePath: storePath, AlreadyExists: true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Stored{}, err
	}

	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return Stored{}, err
	}

	if err := os.Rename(tmpPath, storePath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Stored{Artifact: a, StorePath: storePath, AlreadyExists: true}, nil
		}
		return Stored{}, err
	}

	return Stored{Artifact: a, StorePath: storePath}, nil
}

func copyAndDigest(ctx context.Context, dst io.Writer, src io.Reader) (string, int64, error) {
	h := sha256.New()
	w := io.MultiWriter(dst, h)
	var size int64
	buf := make([]byte, copyBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			written, err := w.Write(buf[:n])
			if err != nil {
				return "", 0, err
			}
			if written != n {
				return "", 0, io.ErrShortWrite
			}
			size += int64(written)
		}
		if errors.Is(readErr, io.EOF) {
			return hex.EncodeToString(h.Sum(nil)), size, nil
		}
		if readErr != nil {
			return "", 0, readErr
		}
	}
}
