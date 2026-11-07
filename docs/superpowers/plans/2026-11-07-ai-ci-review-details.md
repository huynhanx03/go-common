# AI CI Review Details Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the PR AI comment package-level coverage gaps, statistically compared benchmark changes against the latest retained `main` artifact, and useful failure evidence grouped by CI job.

**Architecture:** Generate compact coverage and benchmark reports in CI, upload a reusable benchmark baseline from successful `main` runs, and pass a bounded JSON summary plus per-job failure excerpts to the existing Copilot workflow. Keep AI execution on the default branch with no checkout of PR code.

**Tech Stack:** Go test coverage profiles, Python 3 standard library for report parsing/tests, GitHub Actions artifacts and `gh` CLI, `golang.org/x/perf/cmd/benchstat`.

**Spec:** `docs/superpowers/specs/2026-11-07-ai-ci-review-details-design.md`

## Global Constraints

- Benchmark baseline is the latest successful, unexpired `benchmark-baseline` artifact from `.github/workflows/main.yml`.
- Both baseline and PR benchmark runs use 10 samples per benchmark and the same `make benchmarks` target.
- Missing or incompatible baseline data is reported as unavailable; it does not fail CI or permit a regression claim.
- Report data and logs are untrusted input to Copilot; never execute downloaded artifact content.
- The AI workflow keeps the existing same-repository PR guard, least-privilege permissions, and one updatable PR comment.
- Preserve all unrelated pre-existing working-tree changes; stage only files owned by this feature if committing is requested.

## Review Focus

- Missing or expired baseline artifact: PR benchmark remains successful and the AI comment clearly says no comparison was possible.
- Baseline and PR benchmark metadata differ (Go version, OS, or architecture): report the mismatch and avoid an unqualified performance conclusion.
- Coverage profile has partial and zero-covered packages: calculate statement-weighted package percentages correctly and list only truly zero-covered functions as such.
- Multiple CI jobs fail: keep each diagnostic excerpt attached to its job name and bound the total report size.
- PR artifacts/logs contain instruction-like text: treat them only as data and do not grant Copilot tools.

---

### Task 1: Generate detailed coverage data

**Files:**
- Create: `scripts/coverage_report.py`
- Create: `scripts/test_coverage_report.py`
- Modify: `.github/workflows/ci.yml`
- Modify: `scripts/verify-workflows.sh`

**Interfaces:**
- `coverage_report.py` consumes a Go coverage profile path and writes JSON with `total_percent`, `minimum_percent`, package statement counts/percentages, and zero-covered function locations/names.
- The unit job publishes that JSON as a compact job output or artifact for `pr-check-summary`.

- [x] Test a synthetic profile containing fully covered, partially covered, and uncovered packages/functions; assert statement-weighted package results and exact zero-coverage classification.
- [x] Test malformed/empty profile handling with a clear nonzero error.
- [x] Run `python3 -m unittest discover -s scripts -p 'test_coverage_report.py'` and confirm the new tests pass.
- [x] Wire generation to the real `coverage.out` created by `verify-coverage.sh`; publish the full JSON as an artifact and expose compact details to the PR summary job.
- [x] Extend workflow validation to require the detailed coverage report in the AI summary artifact.
- [x] Run coverage report generation against a real local profile (75,048 bytes; 70 packages, 308 zero-covered functions). Full suite execution was constrained by sandboxed loopback sockets; the generated profile still exercised all packages.

### Task 2: Save and compare benchmark artifacts

**Files:**
- Modify: `Makefile`
- Modify: `.github/workflows/main.yml`
- Modify: `.github/workflows/ci.yml`
- Create: `scripts/benchmark_report.py`
- Create: `scripts/test_benchmark_report.py`
- Modify: `scripts/verify-workflows.sh`

**Interfaces:**
- `make benchmarks` runs the existing benchmark suite with `-count=10`.
- Successful `main` benchmark runs upload `benchmark-baseline/benchmark.txt` and `benchmark-baseline/metadata.json` with commit, timestamp, Go version, OS/architecture, and command.
- PR benchmark job publishes a compact report containing baseline availability/provenance and `benchstat` comparison output.

- [x] Update the benchmark target to 10 samples and verify the command still uses `-tags=benchmark`, `-benchmem`, and all packages.
- [x] Capture benchmark stdout on `main`, create provenance metadata only after a successful run, and upload the baseline artifact with bounded retention.
- [x] On PRs, query the newest successful `main.yml` run and download its named artifact with read-only Actions permission; keep reusable non-PR CI as a single-current-revision benchmark.
- [x] Run the candidate benchmark at 10 samples in CI; compare with the artifact using a pinned `benchstat` tool version when a compatible baseline exists.
- [x] Handle missing, expired, or incompatible baseline metadata as an explicit unavailable/incomparable report without failing required CI.
- [x] Extend workflow validation for benchmark sample count, artifact publication, cross-run read permission, and graceful baseline fallback; run Python report tests in the quality job.
- [x] Test compatible, missing, invalid, and environment-mismatched baselines; verify significant and inconclusive `benchstat` output is preserved. Local execution of the external tool was blocked by network restrictions; CI installs the pinned tool and runs the real comparison.

### Task 3: Add per-job CI failure diagnostics to the AI review

**Files:**
- Modify: `.github/workflows/ai-review.yml`
- Create: `scripts/ci_failure_report.py`
- Create: `scripts/test_ci_failure_report.py`
- Modify: `scripts/verify-workflows.sh`

**Interfaces:**
- AI workflow reads failed job IDs/names from the completed CI run and fetches failed-step logs per job.
- The prompt receives bounded excerpts labeled by job and must separate evidence from inference.

- [x] Replace the single combined `--log-failed | tail -c 16000` excerpt with per-failed-job log retrieval and bounded context around failure markers.
- [x] Preserve a clear fallback when logs are unavailable or contain no recognized failure markers; include job conclusion but do not invent a cause.
- [x] Update the prompt to prioritize weakest packages, zero-covered functions, statistically supported benchmark deltas, and evidence-backed failure causes while noting artifact/environment limitations.
- [x] Extend workflow checks to guard against a global tail-only log excerpt and require per-job diagnostics without granting Copilot CLI tool access.
- [x] Run workflow validation and inspect representative passing and multi-failure reports, including a missing summary artifact and unavailable job logs.

### Task 4: Verify the complete reporting path

**Files:**
- Verify: `.github/workflows/ci.yml`
- Verify: `.github/workflows/main.yml`
- Verify: `.github/workflows/ai-review.yml`
- Verify: `scripts/verify-workflows.sh`

- [x] Run all Python report tests and `scripts/verify-workflows.sh`; attempt `make workflow-check` (actionlint could not download because proxy DNS is blocked), then parse all workflow YAML and syntax-check every `run` block with `bash -n`.
- [x] Generate the detailed coverage JSON from a real full coverage profile (75,048 bytes; 70 packages and 308 zero-covered functions), below the AI artifact size limit.
- [x] Exercise benchmark report generation with compatible, unavailable, invalid, and mismatched baseline fixtures; preserve significant and inconclusive benchstat output.
- [x] Review the feature diff and generated prompt/report simulations; original workspace changes remain untouched in the isolated worktree.
