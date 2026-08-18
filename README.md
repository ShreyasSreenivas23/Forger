# Forger — Mini CI/CD Build Engine

A lightweight, local build-orchestration system written in Go. Forger executes user-defined build steps as a dependency-aware DAG with content-addressable caching and parallel execution.

## Features

- **YAML config** — declare steps with `name`, `run`, `needs`, `inputs`, `outputs`, `env`, `retries`, and `timeout`
- **DAG scheduling** — topological sort, cycle detection, parallel worker pool
- **Content-addressable cache** — deterministic SHA-256 build hashes; skip unchanged jobs
- **Failure handling** — downstream skip on failure, optional `--fail-fast`, per-job retries with backoff
- **CLI** — human-readable tables or `--output json` for CI automation

## Install

```bash
go build -o forger ./cmd/forger
```

## Quick Start

```bash
cd examples
../forger build config.yaml
```

Second run should show cache hits for unchanged steps:

```bash
../forger build config.yaml --verbose
```

## CLI Usage

```
forger build [config-path] [options]

Options:
  -j, --jobs <N>           Max concurrent workers (default: CPU count)
      --no-cache           Bypass cache lookup (still writes cache)
      --only <job>         Build only <job> and its dependencies
      --dry-run            Show plan without executing
      --clean-cache        Purge local cache before running
      --fail-fast          Cancel all jobs on first failure
      --output <fmt>       'text' (default) or 'json'
      --cache-dir <path>   Override cache directory (default .buildcache/)
      --cache-max-size <sz> Max cache size before LRU eviction (e.g. 500MB)
  -v, --verbose            Stream live logs during execution
```

## Config Example

```yaml
steps:
  - name: install
    run: pip install -r requirements.txt
    inputs:
      - requirements.txt
    outputs:
      - .venv/

  - name: test
    run: pytest
    needs: [install]
    inputs:
      - src/**/*.py
    outputs:
      - test-results.xml

  - name: build
    run: python build.py
    needs: [install, test]
    inputs:
      - src/**/*.py
    outputs:
      - dist/
    env:
      BUILD_ENV: production
    retries: 1
    timeout: 300
```

## Cache Layout

```
.buildcache/
  objects/<prefix>/<hash>/   # artifact blobs
  index/<job>-<hash>.json    # metadata
  logs/<job>-<run-id>.log    # per-job logs
```

## Testing

```bash
go test ./...
```

## License

MIT
