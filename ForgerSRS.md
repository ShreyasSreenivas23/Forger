# Software Requirements Specification (SRS)
## Mini CI/CD Build Engine

**Version:** 1.0
**Status:** Draft
**Document Owner:** Engineering
**Date:** 2026-08-17

---

## 1. Introduction

### 1.1 Purpose
This document specifies the functional and non-functional requirements for the **Mini CI/CD Build Engine**, a lightweight, local build-orchestration system that executes user-defined build steps as a dependency-aware directed acyclic graph (DAG), with content-addressable caching and parallel execution. It is intended as a scoped-down analogue of tools like GitHub Actions or Bazel/Buck's build-cache model, targeted at a single machine rather than a distributed cluster.

### 1.2 Scope
The system reads a `config.yaml` describing a set of named build steps and their commands, builds a dependency graph between them, topologically schedules execution across a worker pool, executes each step in a subprocess, computes a deterministic build hash per step to enable cache hits, stores/retrieves artifacts from a local cache, and reports a structured build result (success/failure, logs, timings, cache stats).

**In scope:**
- CLI entry point
- YAML config parsing and validation
- Dependency graph construction + topological sort + cycle detection
- Worker-pool based parallel job execution
- Deterministic build-hash computation (source + deps + command + env)
- Local content-addressable artifact cache
- Failure propagation and configurable retries
- Structured logging (per-job and aggregate)
- Build result summary (exit code, timing, cache hit/miss per job)

**Out of scope (explicitly excluded):**
- Remote/distributed execution or clustering (e.g., Kubernetes)
- GitHub/GitLab/CI-provider integration or webhooks
- Cloud artifact storage / remote cache backends
- Secrets management / vault integration
- Full YAML feature parity with GitHub Actions (matrix builds, reusable workflows, marketplace actions, expressions/contexts, conditionals beyond basic `if`)
- Multi-tenant or multi-user scheduling
- A web UI/dashboard (CLI + local log files only)

### 1.3 Definitions, Acronyms, Abbreviations

| Term | Definition |
|---|---|
| DAG | Directed Acyclic Graph representing job dependencies |
| Job | A single named build step defined in config.yaml |
| Build Hash | A deterministic fingerprint of a job's inputs used for cache lookup |
| Cache Hit | A job whose build hash already exists in the artifact cache; execution is skipped and outputs are restored |
| Cache Miss | A job whose build hash is not found; the job must execute |
| Worker Pool | A bounded set of concurrent execution slots (threads/processes) that run ready jobs |
| Artifact | Any file/directory produced by a job that is persisted for reuse or downstream consumption |
| Topological Order | An ordering of DAG nodes such that every job runs after all its dependencies |
| Ready Job | A job whose dependencies have all completed successfully (or hit cache) |

### 1.4 References
- GitHub Actions workflow syntax (conceptual inspiration only, not implemented)
- Bazel/Buck remote caching model (conceptual inspiration for hash-based caching)

### 1.5 Overview
Section 2 describes the product context and high-level architecture. Section 3 details functional requirements by subsystem. Section 4 covers non-functional requirements. Section 5 defines external interfaces (CLI, config schema, cache format). Section 6 covers data requirements. Section 7 lists constraints and assumptions. Section 8 gives use cases/user stories. Appendices contain the config schema, hash algorithm, and glossary.

---

## 2. Overall Description

### 2.1 Product Perspective
The Build Engine is a standalone local CLI tool with no external service dependencies. It operates entirely on the local filesystem, invoking subprocesses to run user-defined shell commands. It is analogous in spirit to `make`, but with explicit YAML-declared steps, automatic dependency inference (or explicit `needs:` declarations), and a content-hash-based cache similar to Docker layer caching.

### 2.2 High-Level Architecture

```
                 CLI
                  │
                  ▼
             Config Parser
                  │
                  ▼
             Build Planner
                  │
                  ▼
             Dependency DAG
            /       |       \
           ▼        ▼        ▼
        Job A     Job B     Job C
           │        │        │
           └────────┼────────┘
                    ▼
                Job Runner
                    │
             ┌──────┴──────┐
             ▼             ▼
          Cache          Executor
             │             │
             └──────┬──────┘
                    ▼
                Artifacts
```

**Component responsibilities:**

| Component | Responsibility |
|---|---|
| CLI | Parses command-line arguments, invokes the pipeline, prints final result |
| Config Parser | Loads and validates `config.yaml` against the schema; produces an in-memory `BuildSpec` |
| Build Planner | Resolves job dependencies (explicit `needs:` or inferred), constructs the DAG |
| Dependency DAG | Validates acyclicity, computes topological order and ready-sets |
| Job Runner | Consumes ready jobs, dispatches to worker pool, manages job lifecycle/state |
| Cache | Computes/looks up build hashes; stores and restores artifacts |
| Executor | Runs the actual subprocess for a job (when cache misses), captures stdout/stderr/exit code |
| Artifacts | Persisted output files per job, keyed by build hash |

### 2.3 User Classes and Characteristics
- **Primary user:** A developer running builds/tests locally via CLI, expecting fast iterative feedback via caching.
- **Secondary user:** A CI wrapper script that invokes the engine non-interactively (exit codes and structured JSON output must support automation).

### 2.4 Operating Environment
- OS: Linux and macOS (primary); Windows best-effort via WSL.
- Language/runtime: implementation-language-agnostic in this SRS, but assumes a language with solid subprocess, filesystem, and concurrency primitives (e.g., Python, Go, Rust, Node.js).
- No network access required at runtime (fully local).

### 2.5 Design and Implementation Constraints
- Must run on a single machine (no distributed scheduling).
- Cache must be filesystem-based (no external DB required for v1).
- Must not require root/admin privileges.
- Job commands run in a shell (`sh -c` / `bash -c`) inheriting a controlled environment.

### 2.6 Assumptions and Dependencies
- Users provide valid, idempotent build commands (i.e., re-running a job with the same inputs produces the same outputs).
- The filesystem supports standard POSIX file operations and reliable mtimes.
- Job commands are trusted (no sandboxing/security isolation is provided in v1).

---

## 3. Functional Requirements

Each requirement is tagged with a unique ID for traceability: `FR-<Subsystem>-<Number>`.

### 3.1 CLI

- **FR-CLI-1**: The system shall provide a CLI command `build` that accepts a path to a config file (default: `./config.yaml`).
- **FR-CLI-2**: The CLI shall support `--jobs <N>` (or `-j`) to set the worker pool size (default: number of CPU cores).
- **FR-CLI-3**: The CLI shall support `--no-cache` to force execution of all jobs, bypassing cache lookups (but still writing to cache).
- **FR-CLI-4**: The CLI shall support `--only <job-name>` to execute a single job and its transitive dependencies.
- **FR-CLI-5**: The CLI shall support `--dry-run` to print the planned execution order and predicted cache hit/miss status without running any commands.
- **FR-CLI-6**: The CLI shall support `--clean-cache` to purge the local artifact cache.
- **FR-CLI-7**: The CLI shall exit with code `0` on full success, non-zero on any job failure that is not recovered by retry.
- **FR-CLI-8**: The CLI shall support `--output json` to emit a machine-readable build summary to stdout instead of human-readable text.
- **FR-CLI-9**: The CLI shall support `--verbose` to stream live per-job logs to stdout during execution.

### 3.2 Config Parser

- **FR-CFG-1**: The system shall parse a YAML file containing a top-level `steps:` list, each with `name` (required, unique) and `run` (required, shell command).
- **FR-CFG-2**: Each step shall optionally declare `needs:` — a list of step names it depends on. If omitted, the step has no explicit dependencies.
- **FR-CFG-3**: Each step shall optionally declare `inputs:` — a list of file/directory glob patterns that participate in the build hash.
- **FR-CFG-4**: Each step shall optionally declare `outputs:` — a list of file/directory paths to persist as artifacts on success.
- **FR-CFG-5**: Each step shall optionally declare `env:` — a map of environment variables to inject for that step.
- **FR-CFG-6**: Each step shall optionally declare `retries:` (integer, default 0) — number of automatic re-attempts on failure.
- **FR-CFG-7**: The system shall validate the config against a schema and shall report clear, line-referenced errors for: missing required fields, duplicate step names, references to undefined `needs` targets, and malformed YAML.
- **FR-CFG-8**: The system shall reject configs where `needs:` introduces a cycle, reporting the offending cycle path.

### 3.3 Build Planner / Dependency DAG

- **FR-DAG-1**: The system shall construct a DAG where nodes are jobs and edges represent `needs:` relationships.
- **FR-DAG-2**: The system shall detect cycles prior to execution and abort with a descriptive error (listing the cyclic chain) if found.
- **FR-DAG-3**: The system shall compute a valid topological ordering of jobs.
- **FR-DAG-4**: The system shall compute, at any point in execution, the current "ready set" — jobs whose dependencies have all completed (success or cache hit).
- **FR-DAG-5**: When `--only <job>` is used, the planner shall reduce the DAG to the transitive closure of dependencies of `<job>`.

### 3.4 Job Runner / Worker Pool

- **FR-RUN-1**: The system shall maintain a bounded worker pool of size `N` (from `--jobs` or default CPU count) and shall never run more than `N` jobs concurrently.
- **FR-RUN-2**: The runner shall dispatch each newly-ready job to an available worker as soon as one is free.
- **FR-RUN-3**: The runner shall track job state through the lifecycle: `PENDING → READY → RUNNING → (CACHE_HIT | SUCCEEDED | FAILED) → SKIPPED (if upstream failed)`.
- **FR-RUN-4**: On job failure, the runner shall mark all downstream (dependent) jobs as `SKIPPED` and shall not execute them, unless the failed job has remaining retries.
- **FR-RUN-5**: The runner shall support per-job `retries:` with an exponential or fixed backoff (implementation-defined) between attempts.
- **FR-RUN-6**: The runner shall continue executing independent (non-downstream) branches of the DAG even after a failure elsewhere, unless `--fail-fast` is specified.
- **FR-RUN-7**: The system shall support an optional `--fail-fast` flag that immediately cancels all running/pending jobs upon the first failure.
- **FR-RUN-8**: The runner shall record start time, end time, and duration for every job.

### 3.5 Executor

- **FR-EXE-1**: The executor shall run each job's `run:` command in a subprocess shell, with working directory set to the project root (configurable per-step via `cwd:`).
- **FR-EXE-2**: The executor shall merge global environment variables with step-level `env:` overrides (step-level takes precedence).
- **FR-EXE-3**: The executor shall capture stdout and stderr separately (or interleaved, per config) and persist them to a per-job log file.
- **FR-EXE-4**: The executor shall capture and propagate the subprocess exit code as the job's success/failure signal (0 = success).
- **FR-EXE-5**: The executor shall support a configurable per-job timeout; on timeout, the process shall be terminated and the job marked `FAILED` with a timeout reason.

### 3.6 Cache

- **FR-CACHE-1**: The system shall compute a **build hash** for each job as a deterministic function of:
  1. Hash of all files matched by the job's `inputs:` globs (content-based, e.g., SHA-256 of concatenated sorted file hashes)
  2. Build hashes of all direct dependencies (`needs:`)
  3. The exact `run:` command string
  4. The resolved environment variables for the step
- **FR-CACHE-2**: Before executing a job, the system shall look up the build hash in the local cache store.
- **FR-CACHE-3**: On a cache hit, the system shall restore the job's declared `outputs:` from the cache into the working directory without invoking the executor, and shall mark the job `CACHE_HIT`.
- **FR-CACHE-4**: On a cache miss, after successful execution, the system shall store the job's `outputs:` into the cache keyed by the build hash.
- **FR-CACHE-5**: The cache store shall be content-addressable, located at a configurable local directory (default `.buildcache/`).
- **FR-CACHE-6**: The system shall support cache eviction via `--clean-cache` (full purge) and an optional max-size/LRU eviction policy (configurable, e.g., `--cache-max-size`).
- **FR-CACHE-7**: A change to any upstream dependency's build hash shall invalidate (produce a cache miss for) all downstream jobs, even if their own `inputs:` are unchanged — reflecting transitive hash composition per FR-CACHE-1.
- **FR-CACHE-8**: The system shall provide a cache statistics summary at the end of each build (hits, misses, bytes read/written).

### 3.7 Artifacts

- **FR-ART-1**: The system shall persist each job's `outputs:` to the artifact cache upon successful execution.
- **FR-ART-2**: The system shall make a completed job's `outputs:` available on disk (in the working directory) regardless of whether they came from a fresh run or a cache restore, so downstream jobs can consume them uniformly.
- **FR-ART-3**: The system shall preserve file permissions and relative directory structure when storing/restoring artifacts.

### 3.8 Logging & Reporting

- **FR-LOG-1**: The system shall write a per-job log file capturing command, start/end time, exit code, and full stdout/stderr.
- **FR-LOG-2**: The system shall produce a final build summary including: total duration, per-job status table (name, status, duration, cache hit/miss), and overall pass/fail result.
- **FR-LOG-3**: The summary shall be available in both human-readable (table) and `--output json` machine-readable formats.

---

## 4. Non-Functional Requirements

| ID | Category | Requirement |
|---|---|---|
| NFR-1 | Performance | For a DAG of ≤200 jobs on an 8-core machine, full-cache-miss build overhead (excluding actual command runtime) shall not exceed 500ms. |
| NFR-2 | Performance | A fully cached build (all cache hits) of the same DAG shall complete DAG traversal + restore in under 1 second, excluding artifact I/O for very large artifacts. |
| NFR-3 | Scalability | The engine shall correctly handle DAGs with at least 1,000 nodes without stack overflow or quadratic blowups in topological sort (target: O(V+E)). |
| NFR-4 | Reliability | A crash mid-build shall not corrupt the cache; partially-written cache entries shall be detected and treated as misses. |
| NFR-5 | Determinism | Given identical inputs (files, command, env, dependency hashes), the build hash shall be bit-for-bit reproducible across runs and machines. |
| NFR-6 | Usability | Error messages (cycle detection, schema errors, job failures) shall include actionable context (file/line, job name, offending path). |
| NFR-7 | Portability | The engine shall run without modification on Linux and macOS; no OS-specific shell syntax shall be required in the engine itself (though user commands may be shell-specific). |
| NFR-8 | Observability | Every job's log file shall be retrievable independently after the build completes, without needing to re-run. |
| NFR-9 | Resource Safety | The worker pool shall never exceed the configured concurrency limit, even under retry storms. |
| NFR-10 | Testability | Core subsystems (DAG builder, hash computation, cache store) shall be unit-testable in isolation from subprocess execution. |

---

## 5. External Interface Requirements

### 5.1 CLI Interface

```
build [config-path] [options]

Options:
  -j, --jobs <N>          Max concurrent workers (default: CPU count)
      --no-cache          Bypass cache lookup (still writes cache)
      --only <job>         Build only <job> and its dependencies
      --dry-run            Show plan without executing
      --clean-cache        Purge local cache before running
      --fail-fast          Cancel all jobs on first failure
      --output <fmt>       'text' (default) or 'json'
      --cache-dir <path>   Override cache directory (default .buildcache/)
      --cache-max-size <sz> Max cache size before LRU eviction (e.g. "500MB")
  -v, --verbose            Stream live logs during execution
```

### 5.2 Config File Interface (`config.yaml`)

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
      - tests/**/*.py
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

### 5.3 Cache Store Layout (on disk)

```
.buildcache/
  objects/
    <sha256-prefix>/<sha256>/     # content-addressed artifact blobs
  index/
    <job-name>-<build-hash>.json  # metadata: outputs manifest, timestamps
  logs/
    <job-name>-<run-id>.log
```

### 5.4 JSON Output Schema (`--output json`)

```json
{
  "status": "success | failure",
  "duration_ms": 4213,
  "jobs": [
    {
      "name": "install",
      "status": "CACHE_HIT | SUCCEEDED | FAILED | SKIPPED",
      "duration_ms": 0,
      "build_hash": "a1b2c3...",
      "exit_code": 0,
      "retries_used": 0
    }
  ],
  "cache_stats": {
    "hits": 2,
    "misses": 1,
    "bytes_written": 10485760,
    "bytes_read": 2048
  }
}
```

---

## 6. Data Requirements

- **BuildSpec**: in-memory representation of parsed config (steps, dependencies, inputs, outputs, env, retries, timeout).
- **JobNode**: DAG node wrapping a step, its resolved dependency edges, and runtime state.
- **BuildHash**: SHA-256 hex digest per job, computed per FR-CACHE-1.
- **CacheEntry**: metadata mapping a build hash → list of stored artifact blob references + manifest.
- **BuildResult**: aggregate report structure (per-job statuses, timings, cache stats) — see §5.4.

Data retention: job logs and cache entries persist indefinitely on disk until explicitly purged via `--clean-cache` or size-based LRU eviction (if configured).

---

## 7. Constraints and Assumptions

- No sandboxing/isolation of job subprocesses in v1 — jobs run with the same privileges as the invoking user (documented security limitation, not a defect).
- No support for distributed/remote caching in v1; cache is local-disk only.
- Config schema intentionally minimal — no conditionals, matrices, or reusable templates.
- Assumes build commands are deterministic and side-effect-free with respect to declared `inputs:`/`outputs:` (undeclared side effects are not tracked by the cache and may cause stale-cache bugs — documented as a known limitation).

---

## 8. Use Cases / User Stories

**UC-1: First-time full build**
As a developer, I run `build` on a clean checkout with an empty cache, so that all jobs execute in dependency order and populate the cache.

**UC-2: Incremental rebuild after a source change**
As a developer, I modify one file under `test`'s `inputs:` and re-run `build`, so that only `test` and its downstream dependents (e.g., `build`) are cache-misses and re-execute, while `install` remains a cache hit.

**UC-3: Parallel independent branches**
As a developer with two independent lint/test jobs with no shared dependency, I run `build -j 4`, so that both execute concurrently rather than sequentially.

**UC-4: Failure isolation**
As a developer, one job fails; I expect its downstream dependents to be marked `SKIPPED` while unrelated branches still complete, unless I passed `--fail-fast`.

**UC-5: CI automation**
As a CI script, I invoke `build --output json --fail-fast` and parse the JSON result to post a status check, relying on a non-zero exit code to detect failure.

**UC-6: Cache inspection / dry run**
As a developer, I run `build --dry-run` before a real build to preview which jobs will be cache hits vs. misses, so I can predict build time.

---

## 9. Appendix

### 9.1 Build Hash Formula (reference)

```
build_hash(job) = SHA256(
    content_hash(job.inputs)          # sorted, concatenated file hashes
  + build_hash(dep) for dep in job.needs   # transitively composed
  + job.run                          # exact command string
  + canonicalized(job.env)           # sorted key=value pairs
)
```

This ensures: unchanged inputs/command/env/deps → identical hash → cache hit; any change anywhere upstream propagates forward, invalidating all dependents.

### 9.2 Job State Machine

```
PENDING → READY → RUNNING → SUCCEEDED
                         ↘  CACHE_HIT
                         ↘  FAILED → (retry → RUNNING) | SKIPPED (downstream)
```

### 9.3 Glossary
See §1.3.

---

*End of Document*
