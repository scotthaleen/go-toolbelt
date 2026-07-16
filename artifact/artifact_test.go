package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	contents := "hello artifact\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}

	wantHash := sha256.Sum256([]byte(contents))
	if a.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("sha mismatch: got %q", a.SHA256)
	}
	if a.Size != int64(len(contents)) {
		t.Fatalf("size mismatch: got %d", a.Size)
	}
	if a.Name != "input.txt" {
		t.Fatalf("name mismatch: got %q", a.Name)
	}
}

func TestStorePutCopiesByContentHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	contents := "stored artifact\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	store := Store{Root: filepath.Join(dir, "store")}
	stored, err := store.Put(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if stored.AlreadyExists {
		t.Fatal("first put should not report AlreadyExists")
	}
	if !strings.HasPrefix(stored.StorePath, filepath.Join(store.Root, stored.SHA256[:2])) {
		t.Fatalf("store path is not sharded by hash: %s", stored.StorePath)
	}

	got, err := os.ReadFile(stored.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Fatalf("stored contents mismatch: %q", string(got))
	}

	again, err := store.Put(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyExists {
		t.Fatal("second put should report AlreadyExists")
	}
	if again.StorePath != stored.StorePath {
		t.Fatalf("stored path changed: %q != %q", again.StorePath, stored.StorePath)
	}
}

func TestStorePutRequiresRoot(t *testing.T) {
	_, err := (Store{}).Put(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStorePathValidatesDigest(t *testing.T) {
	store := Store{Root: t.TempDir()}
	digest := strings.Repeat("ab", sha256.Size)

	got, err := store.Path(digest)
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	want := filepath.Join(store.Root, digest[:2], digest)
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}

	for _, digest := range []string{"../outside", strings.Repeat("g", sha256.Size*2)} {
		if _, err := store.Path(digest); err == nil {
			t.Errorf("Path(%q) error = nil, want invalid digest error", digest)
		}
	}
}
