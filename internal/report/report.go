package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/forger/buildengine/internal/cache"
)

// JobResult is the per-job outcome in a build.
type JobResult struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	DurationMS  int64  `json:"duration_ms"`
	BuildHash   string `json:"build_hash,omitempty"`
	ExitCode    int    `json:"exit_code,omitempty"`
	RetriesUsed int    `json:"retries_used"`
}

// BuildResult is the aggregate build report.
type BuildResult struct {
	Status     string      `json:"status"`
	DurationMS int64       `json:"duration_ms"`
	Jobs       []JobResult `json:"jobs"`
	CacheStats cache.Stats `json:"cache_stats"`
}

// Print outputs the build result in the requested format.
func Print(w io.Writer, result *BuildResult, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		return printText(w, result)
	}
}

func printText(w io.Writer, result *BuildResult) error {
	fmt.Fprintf(w, "\nBuild %s in %s\n\n", strings.ToUpper(result.Status), formatDuration(result.DurationMS))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSTATUS\tDURATION\tCACHE")
	for _, job := range result.Jobs {
		cacheCol := "-"
		switch job.Status {
		case "CACHE_HIT":
			cacheCol = "hit"
		case "SUCCEEDED", "FAILED":
			cacheCol = "miss"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", job.Name, job.Status, formatDuration(job.DurationMS), cacheCol)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\nCache: %d hits, %d misses, %s read, %s written\n",
		result.CacheStats.Hits,
		result.CacheStats.Misses,
		formatBytes(result.CacheStats.BytesRead),
		formatBytes(result.CacheStats.BytesWritten),
	)
	return nil
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return time.Duration(ms * int64(time.Millisecond)).Round(time.Millisecond).String()
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
