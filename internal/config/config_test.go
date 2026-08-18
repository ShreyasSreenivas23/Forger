package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `steps:
  - name: build
    run: echo hello
    needs: [lint]
  - name: lint
    run: echo lint
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	spec, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(spec.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(spec.Steps))
	}
}

func TestDuplicateName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `steps:
  - name: build
    run: echo a
  - name: build
    run: echo b
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestUndefinedNeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `steps:
  - name: build
    run: echo a
    needs: [missing]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected undefined needs error")
	}
}
