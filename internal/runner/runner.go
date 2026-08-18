package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/forger/buildengine/internal/cache"
	"github.com/forger/buildengine/internal/config"
	"github.com/forger/buildengine/internal/dag"
	"github.com/forger/buildengine/internal/executor"
	"github.com/forger/buildengine/internal/hash"
	"github.com/forger/buildengine/internal/report"
)

// Options configures a build run.
type Options struct {
	Spec        *config.BuildSpec
	Graph       *dag.Graph
	Workers     int
	NoCache     bool
	DryRun      bool
	FailFast    bool
	Verbose     bool
	CacheDir    string
	CacheMaxSz  int64
	GlobalEnv   map[string]string
	ProjectRoot string
}

// Run executes the build pipeline and returns the result.
func Run(ctx context.Context, opts Options) (*report.BuildResult, error) {
	if opts.ProjectRoot == "" {
		opts.ProjectRoot, _ = os.Getwd()
	}
	if opts.Workers <= 0 {
		opts.Workers = 1
	}

	cacheStore, err := cache.NewStore(opts.CacheDir, opts.CacheMaxSz)
	if err != nil {
		return nil, err
	}

	buildHashes, err := hash.ComputeAllBuildHashes(opts.Spec, opts.Graph.Order, opts.ProjectRoot, opts.GlobalEnv)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	jobResults := make(map[string]*report.JobResult)
	var mu sync.Mutex

	for _, name := range opts.Graph.Order {
		jobResults[name] = &report.JobResult{Name: name, BuildHash: buildHashes[name]}
	}

	if opts.DryRun {
		for _, name := range opts.Graph.Order {
			jr := jobResults[name]
			if !opts.NoCache {
				if _, ok := cacheStore.Lookup(name, buildHashes[name]); ok {
					jr.Status = string(dag.StatusCacheHit)
				} else {
					jr.Status = string(dag.StatusSucceeded)
					cacheStore.RecordMiss()
				}
			} else {
				jr.Status = string(dag.StatusSucceeded)
				cacheStore.RecordMiss()
			}
		}
		return finalize(start, jobResults, opts.Graph.Order, cacheStore.Stats), nil
	}

	runID := fmt.Sprintf("%d", start.UnixNano())
	failFast := make(chan struct{})
	var failOnce sync.Once

	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Workers)

	for {
		select {
		case <-ctx.Done():
			return finalize(start, jobResults, opts.Graph.Order, cacheStore.Stats), ctx.Err()
		default:
		}

		select {
		case <-failFast:
			markRemainingSkipped(opts.Graph, jobResults, &mu)
			wg.Wait()
			return finalize(start, jobResults, opts.Graph.Order, cacheStore.Stats), nil
		default:
		}

		ready := opts.Graph.ReadyJobs()
		if len(ready) == 0 {
			if opts.Graph.AllTerminal() {
				break
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		for _, name := range ready {
			select {
			case <-failFast:
				break
			default:
			}

			node := opts.Graph.Nodes[name]
			node.Status = dag.StatusReady

			wg.Add(1)
			sem <- struct{}{}

			go func(jobName string) {
				defer wg.Done()
				defer func() { <-sem }()

				jr := jobResults[jobName]
				step := opts.Graph.Nodes[jobName].Step
				bh := buildHashes[jobName]

				// Check cache
				if !opts.NoCache {
					if entry, ok := cacheStore.Lookup(jobName, bh); ok {
						workDir := resolveWorkDir(opts.ProjectRoot, step.CWD)
						if err := cacheStore.Restore(entry, workDir); err == nil {
							mu.Lock()
							opts.Graph.Nodes[jobName].Status = dag.StatusCacheHit
							jr.Status = string(dag.StatusCacheHit)
							jr.ExitCode = 0
							mu.Unlock()
							if opts.Verbose {
								fmt.Printf("[%s] CACHE_HIT\n", jobName)
							}
							return
						}
					}
				}

				opts.Graph.Nodes[jobName].Status = dag.StatusRunning
				maxAttempts := step.Retries + 1
				var lastResult executor.Result

				for attempt := 0; attempt < maxAttempts; attempt++ {
					select {
					case <-failFast:
						mu.Lock()
						opts.Graph.Nodes[jobName].Status = dag.StatusSkipped
						jr.Status = string(dag.StatusSkipped)
						mu.Unlock()
						return
					case <-ctx.Done():
						mu.Lock()
						opts.Graph.Nodes[jobName].Status = dag.StatusFailed
						jr.Status = string(dag.StatusFailed)
						mu.Unlock()
						return
					default:
					}

					if attempt > 0 {
						jr.RetriesUsed++
						backoff := time.Duration(attempt) * time.Second
						time.Sleep(backoff)
					}

					jobStart := time.Now()
					execCtx := ctx
					var cancel context.CancelFunc
					if step.Timeout > 0 {
						execCtx, cancel = context.WithTimeout(ctx, time.Duration(step.Timeout)*time.Second)
					}

					mergedEnv := mergeEnv(opts.GlobalEnv, step.Env)
					lastResult = executor.Run(execCtx, executor.Options{
						Command: step.Run,
						WorkDir: resolveWorkDir(opts.ProjectRoot, step.CWD),
						Env:     mergedEnv,
						Timeout: time.Duration(step.Timeout) * time.Second,
					})
					if cancel != nil {
						cancel()
					}

					jr.DurationMS = time.Since(jobStart).Milliseconds()
					jr.ExitCode = lastResult.ExitCode

					logPath := cacheStore.LogPath(jobName, runID)
					writeLog(logPath, step, lastResult, jobStart)

					if opts.Verbose {
						fmt.Printf("[%s] %s\n", jobName, lastResult.Stdout)
						if lastResult.Stderr != "" {
							fmt.Fprintf(os.Stderr, "[%s] %s\n", jobName, lastResult.Stderr)
						}
					}

					if lastResult.ExitCode == 0 {
						workDir := resolveWorkDir(opts.ProjectRoot, step.CWD)
						if err := cacheStore.Store(jobName, bh, step.Outputs, workDir); err != nil {
							mu.Lock()
							opts.Graph.Nodes[jobName].Status = dag.StatusFailed
							jr.Status = string(dag.StatusFailed)
							mu.Unlock()
							triggerFailFast(opts.FailFast, failFast, &failOnce)
							opts.Graph.MarkDownstreamSkipped(jobName)
							return
						}

						mu.Lock()
						opts.Graph.Nodes[jobName].Status = dag.StatusSucceeded
						jr.Status = string(dag.StatusSucceeded)
						mu.Unlock()
						return
					}
				}

				mu.Lock()
				opts.Graph.Nodes[jobName].Status = dag.StatusFailed
				jr.Status = string(dag.StatusFailed)
				mu.Unlock()

				triggerFailFast(opts.FailFast, failFast, &failOnce)
				opts.Graph.MarkDownstreamSkipped(jobName)
			}(name)
		}

		if opts.Graph.AllTerminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()
	return finalize(start, jobResults, opts.Graph.Order, cacheStore.Stats), nil
}

func finalize(start time.Time, results map[string]*report.JobResult, order []string, stats cache.Stats) *report.BuildResult {
	jobs := make([]report.JobResult, 0, len(order))
	status := "success"
	for _, name := range order {
		jr := *results[name]
		if jr.Status == "" {
			jr.Status = string(dag.StatusSkipped)
		}
		if jr.Status == string(dag.StatusFailed) {
			status = "failure"
		}
		jobs = append(jobs, jr)
	}
	return &report.BuildResult{
		Status:     status,
		DurationMS: time.Since(start).Milliseconds(),
		Jobs:       jobs,
		CacheStats: stats,
	}
}

func markRemainingSkipped(g *dag.Graph, results map[string]*report.JobResult, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	for name, node := range g.Nodes {
		if node.Status == dag.StatusPending || node.Status == dag.StatusReady || node.Status == dag.StatusRunning {
			node.Status = dag.StatusSkipped
			results[name].Status = string(dag.StatusSkipped)
		}
	}
}

func triggerFailFast(enabled bool, ch chan struct{}, once *sync.Once) {
	if enabled {
		once.Do(func() { close(ch) })
	}
}

func resolveWorkDir(root, cwd string) string {
	if cwd == "" {
		return root
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	return filepath.Join(root, cwd)
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

func writeLog(path string, step config.Step, result executor.Result, start time.Time) {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "=== Job: %s ===\n", step.Name)
	fmt.Fprintf(f, "Command: %s\n", step.Run)
	fmt.Fprintf(f, "Start: %s\n", start.Format(time.RFC3339))
	fmt.Fprintf(f, "Duration: %s\n", result.Duration)
	fmt.Fprintf(f, "Exit Code: %d\n", result.ExitCode)
	if result.TimedOut {
		fmt.Fprintln(f, "Reason: timeout")
	}
	fmt.Fprintln(f, "--- stdout ---")
	fmt.Fprint(f, result.Stdout)
	fmt.Fprintln(f, "--- stderr ---")
	fmt.Fprint(f, result.Stderr)
}
