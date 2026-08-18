package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/forger/buildengine/internal/cache"
	"github.com/forger/buildengine/internal/config"
	"github.com/forger/buildengine/internal/dag"
	"github.com/forger/buildengine/internal/report"
	"github.com/forger/buildengine/internal/runner"
)

const version = "1.0.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	cmd := args[0]
	cmdArgs := args[1:]

	switch cmd {
	case "build":
		return runBuild(cmdArgs)
	case "version", "--version", "-V":
		fmt.Printf("forger %s\n", version)
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		// Treat bare path as config file for convenience
		return runBuild(append([]string{cmd}, cmdArgs...))
	}
}

type buildFlags struct {
	configPath  string
	jobs        int
	noCache     bool
	only        string
	dryRun      bool
	cleanCache  bool
	failFast    bool
	output      string
	cacheDir    string
	cacheMaxSz  string
	verbose     bool
}

func runBuild(args []string) int {
	flags := buildFlags{
		configPath: "./config.yaml",
		jobs:       runtime.NumCPU(),
		output:     "text",
		cacheDir:   ".buildcache",
	}

	i := 0
	for i < len(args) {
		arg := args[i]
		switch arg {
		case "-j", "--jobs":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --jobs requires a value")
				return 1
			}
			if _, err := fmt.Sscanf(args[i], "%d", &flags.jobs); err != nil || flags.jobs < 1 {
				fmt.Fprintln(os.Stderr, "error: invalid --jobs value")
				return 1
			}
		case "--no-cache":
			flags.noCache = true
		case "--only":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --only requires a job name")
				return 1
			}
			flags.only = args[i]
		case "--dry-run":
			flags.dryRun = true
		case "--clean-cache":
			flags.cleanCache = true
		case "--fail-fast":
			flags.failFast = true
		case "--output":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --output requires a format")
				return 1
			}
			flags.output = args[i]
		case "--cache-dir":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --cache-dir requires a path")
				return 1
			}
			flags.cacheDir = args[i]
		case "--cache-max-size":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --cache-max-size requires a size")
				return 1
			}
			flags.cacheMaxSz = args[i]
		case "-v", "--verbose":
			flags.verbose = true
		case "-h", "--help":
			printBuildUsage()
			return 0
		default:
			if arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "error: unknown flag %s\n", arg)
				return 1
			}
			flags.configPath = arg
		}
		i++
	}

	var cacheMaxBytes int64
	if flags.cacheMaxSz != "" {
		var err error
		cacheMaxBytes, err = cache.ParseSize(flags.cacheMaxSz)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	if flags.cleanCache {
		store, err := cache.NewStore(flags.cacheDir, cacheMaxBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := store.Clean(); err != nil {
			fmt.Fprintf(os.Stderr, "error: clean cache: %v\n", err)
			return 1
		}
		if flags.dryRun {
			fmt.Println("Cache cleaned.")
		}
	}

	spec, err := config.Load(flags.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	graph, err := dag.Build(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if flags.only != "" {
		graph, err = graph.Subgraph(flags.only)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	projectRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	result, err := runner.Run(context.Background(), runner.Options{
		Spec:        spec,
		Graph:       graph,
		Workers:     flags.jobs,
		NoCache:     flags.noCache,
		DryRun:      flags.dryRun,
		FailFast:    flags.failFast,
		Verbose:     flags.verbose,
		CacheDir:    flags.cacheDir,
		CacheMaxSz:  cacheMaxBytes,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if err := report.Print(os.Stdout, result, flags.output); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if result.Status != "success" {
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Println(`Forger — Mini CI/CD Build Engine

Usage:
  forger build [config-path] [options]
  forger [config-path] [options]    (shorthand for build)

Commands:
  build     Run the build pipeline
  version   Print version
  help      Show this help`)
	printBuildUsage()
}

func printBuildUsage() {
	fmt.Println(`
Build options:
  -j, --jobs <N>           Max concurrent workers (default: CPU count)
      --no-cache           Bypass cache lookup (still writes cache)
      --only <job>         Build only <job> and its dependencies
      --dry-run            Show plan without executing
      --clean-cache        Purge local cache before running
      --fail-fast          Cancel all jobs on first failure
      --output <fmt>       'text' (default) or 'json'
      --cache-dir <path>   Override cache directory (default .buildcache/)
      --cache-max-size <sz> Max cache size before LRU eviction (e.g. 500MB)
  -v, --verbose            Stream live logs during execution`)
}
