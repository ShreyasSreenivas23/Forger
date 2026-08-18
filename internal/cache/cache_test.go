package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreAndRestore(t *testing.T) {
	dir := t.TempDir()
	workDir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")

	store, err := NewStore(cacheDir, 0)
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(workDir, "dist")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(outDir, "app.txt")
	if err := os.WriteFile(outFile, []byte("artifact"), 0644); err != nil {
		t.Fatal(err)
	}

	hash := "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678"
	if err := store.Store("build", hash, []string{"dist/"}, workDir); err != nil {
		t.Fatal(err)
	}

	entry, ok := store.Lookup("build", hash)
	if !ok {
		t.Fatal("expected cache hit on lookup")
	}

	restoreDir := t.TempDir()
	if err := store.Restore(entry, restoreDir); err != nil {
		t.Fatal(err)
	}

	restored := filepath.Join(restoreDir, "dist", "app.txt")
	data, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "artifact" {
		t.Fatalf("expected artifact, got %q", data)
	}
}

func TestIncompleteEntryIsMiss(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 0)
	if err != nil {
		t.Fatal(err)
	}

	indexDir := filepath.Join(dir, "index")
	if err := os.MkdirAll(indexDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(indexDir, "job-hash.json")
	if err := os.WriteFile(path, []byte(`{"complete":false}`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.Lookup("job", "hash"); ok {
		t.Fatal("incomplete entry should be a miss")
	}
}

func TestParseSize(t *testing.T) {
	sz, err := ParseSize("500MB")
	if err != nil {
		t.Fatal(err)
	}
	if sz != 500*1024*1024 {
		t.Fatalf("got %d", sz)
	}
}
