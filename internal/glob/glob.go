package glob

import (
	"os"
	"path/filepath"
	"strings"
)

// MatchFiles returns all files matching any of the glob patterns relative to root.
// Supports ** for recursive directory matching.
func MatchFiles(root string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	var files []string

	for _, pattern := range patterns {
		matches, err := expandPattern(root, pattern)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				rel = m
			}
			rel = filepath.ToSlash(rel)
			if !seen[rel] {
				seen[rel] = true
				files = append(files, rel)
			}
		}
	}

	return files, nil
}

func expandPattern(root, pattern string) ([]string, error) {
	pattern = filepath.FromSlash(pattern)

	if strings.Contains(pattern, "**") {
		return matchDoubleStar(root, pattern)
	}

	full := filepath.Join(root, pattern)
	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if info.IsDir() {
			err = filepath.WalkDir(m, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() {
					result = append(result, path)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		} else {
			result = append(result, m)
		}
	}
	return result, nil
}

func matchDoubleStar(root, pattern string) ([]string, error) {
	parts := strings.Split(filepath.ToSlash(pattern), "**")
	if len(parts) != 2 {
		return expandPattern(root, pattern)
	}

	prefix := strings.Trim(parts[0], "/")
	suffix := strings.Trim(parts[1], "/")

	var searchRoots []string
	if prefix == "" || prefix == "." {
		searchRoots = []string{root}
	} else {
		full := filepath.Join(root, prefix)
		matches, err := filepath.Glob(full)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			if info, err := os.Stat(full); err == nil && info.IsDir() {
				searchRoots = []string{full}
			}
		} else {
			searchRoots = matches
		}
	}

	var result []string
	for _, sr := range searchRoots {
		err := filepath.WalkDir(sr, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(sr, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if suffix == "" || matchSuffix(rel, suffix) {
				result = append(result, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func matchSuffix(path, suffix string) bool {
	if suffix == "" {
		return true
	}
	if strings.HasPrefix(suffix, "/") {
		suffix = suffix[1:]
	}
	matched, _ := filepath.Match(suffix, path)
	if matched {
		return true
	}
	if strings.Contains(suffix, "*") {
		parts := strings.Split(suffix, "/")
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			base := filepath.Base(path)
			matched, _ = filepath.Match(last, base)
			return matched
		}
	}
	return strings.HasSuffix(path, suffix)
}
