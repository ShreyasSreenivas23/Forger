package cache

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Stats tracks cache hit/miss and I/O metrics.
type Stats struct {
	Hits         int   `json:"hits"`
	Misses       int   `json:"misses"`
	BytesWritten int64 `json:"bytes_written"`
	BytesRead    int64 `json:"bytes_read"`
}

// Entry metadata stored in the index.
type Entry struct {
	JobName    string    `json:"job_name"`
	BuildHash  string    `json:"build_hash"`
	Outputs    []string  `json:"outputs"`
	ObjectPath string    `json:"object_path"`
	CreatedAt  time.Time `json:"created_at"`
	Complete   bool      `json:"complete"`
}

// Store is a content-addressable local artifact cache.
type Store struct {
	Dir     string
	MaxSize int64
	Stats   Stats
}

// NewStore creates or opens a cache at the given directory.
func NewStore(dir string, maxSize int64) (*Store, error) {
	for _, sub := range []string{"objects", "index", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return nil, fmt.Errorf("cache: create %s: %w", sub, err)
		}
	}
	return &Store{Dir: dir, MaxSize: maxSize}, nil
}

// Lookup returns a cache entry for the given job and build hash.
func (s *Store) Lookup(jobName, buildHash string) (*Entry, bool) {
	path := s.indexPath(jobName, buildHash)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}
	if !entry.Complete {
		return nil, false
	}
	objPath := filepath.Join(s.Dir, entry.ObjectPath)
	if _, err := os.Stat(objPath); os.IsNotExist(err) {
		return nil, false
	}
	return &entry, true
}

// Restore copies cached artifacts into the working directory.
func (s *Store) Restore(entry *Entry, workDir string) error {
	src := filepath.Join(s.Dir, entry.ObjectPath)
	var bytesRead int64

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dest := filepath.Join(workDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		n, err := copyFile(path, dest)
		bytesRead += n
		return err
	})
	s.Stats.BytesRead += bytesRead
	s.Stats.Hits++
	return err
}

// Store saves job outputs into the cache keyed by build hash.
func (s *Store) Store(jobName, buildHash string, outputs []string, workDir string) error {
	objectRel := objectRelPath(buildHash)
	objectDir := filepath.Join(s.Dir, objectRel)

	if err := os.RemoveAll(objectDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return err
	}

	// Marker for jobs with no outputs so cache hits still work.
	if len(outputs) == 0 {
		if err := os.WriteFile(filepath.Join(objectDir, ".empty"), []byte{}, 0644); err != nil {
			return err
		}
	}

	var bytesWritten int64
	storedOutputs := make([]string, 0, len(outputs))

	for _, out := range outputs {
		src := filepath.Join(workDir, filepath.FromSlash(out))
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		storedOutputs = append(storedOutputs, out)

		dest := filepath.Join(objectDir, filepath.FromSlash(out))
		if info.IsDir() {
			n, err := copyTree(src, dest)
			bytesWritten += n
			if err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			n, err := copyFile(src, dest)
			bytesWritten += n
			if err != nil {
				return err
			}
		}
	}

	entry := Entry{
		JobName:    jobName,
		BuildHash:  buildHash,
		Outputs:    storedOutputs,
		ObjectPath: objectRel,
		CreatedAt:  time.Now().UTC(),
		Complete:   true,
	}

	indexPath := s.indexPath(jobName, buildHash)
	tmpPath := indexPath + ".tmp"
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		return err
	}

	s.Stats.BytesWritten += bytesWritten
	s.Stats.Misses++

	if s.MaxSize > 0 {
		if err := s.evictLRU(); err != nil {
			return err
		}
	}
	return nil
}

// RecordMiss increments miss counter without storing.
func (s *Store) RecordMiss() {
	s.Stats.Misses++
}

// Clean removes all cache contents.
func (s *Store) Clean() error {
	for _, sub := range []string{"objects", "index", "logs"} {
		path := filepath.Join(s.Dir, sub)
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

// LogPath returns the path for a per-job log file.
func (s *Store) LogPath(jobName, runID string) string {
	safe := strings.ReplaceAll(jobName, "/", "_")
	return filepath.Join(s.Dir, "logs", fmt.Sprintf("%s-%s.log", safe, runID))
}

func (s *Store) indexPath(jobName, buildHash string) string {
	safe := strings.ReplaceAll(jobName, "/", "_")
	return filepath.Join(s.Dir, "index", fmt.Sprintf("%s-%s.json", safe, buildHash))
}

func objectRelPath(buildHash string) string {
	if len(buildHash) >= 2 {
		return filepath.Join("objects", buildHash[:2], buildHash)
	}
	return filepath.Join("objects", buildHash)
}

func copyFile(src, dest string) (int64, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return 0, err
	}

	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, in)
	return n, err
}

func copyTree(src, dest string) (int64, error) {
	var total int64
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		n, err := copyFile(path, target)
		total += n
		_ = info
		return err
	})
	return total, err
}

type lruItem struct {
	path    string
	size    int64
	created time.Time
}

func (s *Store) evictLRU() error {
	var items []lruItem
	var total int64

	err := filepath.WalkDir(filepath.Join(s.Dir, "objects"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != filepath.Join(s.Dir, "objects") {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			size := dirSize(path)
			items = append(items, lruItem{path: path, size: size, created: info.ModTime()})
			total += size
		}
		return nil
	})
	if err != nil {
		return err
	}

	if total <= s.MaxSize {
		return nil
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].created.Before(items[j].created)
	})

	for _, item := range items {
		if total <= s.MaxSize {
			break
		}
		if err := os.RemoveAll(item.path); err != nil {
			return err
		}
		total -= item.size
	}
	return nil
}

func dirSize(path string) int64 {
	var size int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			size += info.Size()
		}
		return nil
	})
	return size
}

// ParseSize parses human-readable size strings like "500MB".
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	multipliers := []struct {
		suffix string
		mult   int64
	}{
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}

	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, m.suffix))
			var num float64
			if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
				return 0, fmt.Errorf("invalid cache size: %s", s)
			}
			return int64(num * float64(m.mult)), nil
		}
	}
	return 0, fmt.Errorf("invalid cache size: %s", s)
}
