package hash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/forger/buildengine/internal/config"
)

func TestBuildHashDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	step := config.Step{
		Name:   "build",
		Run:    "echo build",
		Inputs: []string{"input.txt"},
		Env:    map[string]string{"FOO": "bar"},
	}

	h1, err := ComputeBuildHash(step, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ComputeBuildHash(step, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %s vs %s", h1, h2)
	}
}

func TestBuildHashChangesWithInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}

	step := config.Step{
		Name:   "build",
		Run:    "echo build",
		Inputs: []string{"input.txt"},
	}

	h1, _ := ComputeBuildHash(step, dir, nil)
	if err := os.WriteFile(path, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	h2, _ := ComputeBuildHash(step, dir, nil)
	if h1 == h2 {
		t.Fatal("expected different hashes after input change")
	}
}

func TestTransitiveDepHash(t *testing.T) {
	dir := t.TempDir()
	step := config.Step{Name: "child", Run: "echo child", Needs: []string{"parent"}}
	h, err := ComputeBuildHash(step, dir, map[string]string{"parent": "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ComputeBuildHash(step, dir, map[string]string{"parent": "def456"})
	if err != nil {
		t.Fatal(err)
	}
	if h == h2 {
		t.Fatal("expected different hashes when dep hash changes")
	}
}
