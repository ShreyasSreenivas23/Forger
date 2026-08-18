package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forger/buildengine/internal/config"
	"github.com/forger/buildengine/internal/glob"
)

// ComputeBuildHash computes a deterministic build hash per SRS §9.1.
func ComputeBuildHash(step config.Step, root string, depHashes map[string]string) (string, error) {
	inputHash, err := contentHash(step.Inputs, root)
	if err != nil {
		return "", fmt.Errorf("hash inputs for %s: %w", step.Name, err)
	}

	deps := append([]string(nil), step.Needs...)
	sort.Strings(deps)

	h := sha256.New()
	h.Write([]byte(inputHash))

	for _, dep := range deps {
		dh, ok := depHashes[dep]
		if !ok {
			return "", fmt.Errorf("hash %s: missing dependency hash for '%s'", step.Name, dep)
		}
		h.Write([]byte(dh))
	}

	h.Write([]byte(step.Run))
	h.Write([]byte(canonicalizeEnv(step.Env)))

	return hex.EncodeToString(h.Sum(nil)), nil
}

func contentHash(patterns []string, root string) (string, error) {
	files, err := glob.MatchFiles(root, patterns)
	if err != nil {
		return "", err
	}
	sort.Strings(files)

	h := sha256.New()
	for _, rel := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		fh, err := fileHash(full)
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte(fh))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func canonicalizeEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(env[k])
		b.WriteByte('\n')
	}
	return b.String()
}

// ComputeAllBuildHashes computes build hashes for all steps in dependency order.
func ComputeAllBuildHashes(spec *config.BuildSpec, order []string, root string, globalEnv map[string]string) (map[string]string, error) {
	stepMap := spec.StepByName()
	hashes := make(map[string]string)

	for _, name := range order {
		step := stepMap[name]
		mergedEnv := mergeEnv(globalEnv, step.Env)
		step.Env = mergedEnv
		h, err := ComputeBuildHash(step, root, hashes)
		if err != nil {
			return nil, err
		}
		hashes[name] = h
	}
	return hashes, nil
}

func mergeEnv(global, step map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range global {
		out[k] = v
	}
	for k, v := range step {
		out[k] = v
	}
	return out
}
