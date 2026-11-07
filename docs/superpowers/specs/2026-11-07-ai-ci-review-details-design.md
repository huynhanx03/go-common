# AI CI Review Details Design

## Intent

Make the existing PR AI comment useful for locating quality gaps and performance regressions, rather than only repeating the overall CI status. The report must show package-level coverage, functions with no coverage, benchmark changes against the old base revision, and actionable failure evidence.

## Goals

- Include exact total and per-package statement coverage, plus zero-covered functions, from the coverage profile produced by the existing unit test run.
- Compare per-package coverage with the latest successful `main` coverage artifact and expose added, removed, improved, and regressed packages.
- Compare the PR benchmark output with the latest successful `main` benchmark artifact using `benchstat` statistical comparison.
- Give Copilot diagnostics grouped by failed CI job and selected around error evidence, instead of one arbitrary tail of combined workflow logs.
- Give Copilot bounded PR title, description, branch/commit, and changed-file path context so follow-up recommendations target the affected area.
- Preserve one updatable PR comment and keep the AI workflow from checking out or executing PR code.

## Non-goals

- Add source-diff review or inline code comments.
- Establish a permanent benchmark history service or compare against a different runner's latest `main` result.
- Send full raw logs, source diffs, or every coverage line into the AI prompt; retain full logs in the Actions run for inspection.

## Approaches

1. **Compare with the latest successful `main` benchmark artifact (selected).** Store the raw benchmark output and provenance from each successful `main` benchmark run, then compare the PR result with the newest retained artifact using `golang.org/x/perf/cmd/benchstat`. This avoids rerunning the baseline on every PR, but results may include runner and time variation.
2. **Re-run the base revision in each PR job.** This controls the environment more closely but nearly doubles benchmark work for every PR and does not reuse a previous result.
3. **Send complete combined workflow logs to Copilot.** This avoids parsing, but consumes prompt space and can hide the useful failure among unrelated successful output. Per-job diagnostics are more compact and traceable.

## Design

The unit-coverage step will produce a detailed JSON artifact from the actual `coverprofile`: overall coverage and threshold, statement-weighted package percentages, and functions at 0% coverage. It will keep package statement counts so the AI can prioritize large gaps over tiny packages. The PR summary embeds every package and the 100 zero-covered functions from the weakest-coverage packages first, with total and omitted counts; the full coverage artifact retains every function for inspection. The `main` workflow will publish a 90-day package-coverage baseline with commit and Go environment metadata. The default-branch AI workflow compares the PR report against the newest retained baseline and reports percentage-point changes, new/removed packages, and environment mismatches. Missing coverage history stays an explicit unavailable result.

The `main` benchmark workflow will upload a `benchmark-baseline` artifact after a successful benchmark run. It will contain raw output and metadata for the commit, workflow run, Go version, OS/architecture, command, sample count, and creation time, with 90-day retention. Both main and PR benchmark targets use 20 samples and the benchmark jobs have a 60-minute timeout. For pull-request runs, the benchmark job searches recent successful `main` runs for the newest retained artifact, supporting both the new artifact format and the existing `go-common-benchmark-<sha>` artifact. The legacy format has raw output only, so the job reconstructs provenance from the run commit and its `go.mod`/`Makefile`; its recorded sample count can differ from the current 20-sample PR run. The report preserves both counts and the AI must call out a mismatch when interpreting results. Candidate output, metadata, and comparison JSON are retained together in a short-lived `benchmark-comparison` artifact. Non-PR reusable CI runs retain the current single-revision benchmark behavior. If no unexpired baseline artifact is available, the PR report explicitly says comparison is unavailable and will not fail CI or infer a regression. The AI must distinguish statistically significant deltas from inconclusive results and mention that runs from different times/runners can be noisy.

The AI workflow will list failed jobs from the completed CI run and fetch failed-step logs for each job individually. A tested helper will extract bounded excerpts around failure markers per job, preserving the job name and a clear unavailable/no-match status. The workflow checks out only the repository default branch to run this helper and grants `contents: read` for that checkout; it never checks out or executes PR code. If logs cannot be fetched, the AI will report the failed job status and link the Actions run without guessing at a cause. Full logs remain available in GitHub Actions.

The `pr-check-summary` artifact will combine check statuses, detailed coverage and its execution metadata, benchmark comparison, and failure diagnostics. The default-branch Copilot workflow adds package coverage deltas from the main artifact and bounded PR context (title/body, base/head commits, and up to 40 changed paths). It never checks out or executes PR code. The prompt requires structured English sections for CI checks, tests/coverage, benchmarks, failures, and prioritized code/test follow-ups. Recommendations must cite a specific package, function, test, benchmark, or changed path and avoid invented source details. The AI workflow retries artifact listing and download for a bounded period to tolerate Actions artifact visibility delays; if the artifact is still unavailable, its fallback remains limited to workflow job statuses.

## Constraints and risks

- Existing successful `main` runs may have only a legacy raw benchmark artifact. The PR job must find the newest usable retained artifact and preserve its actual sample count; future main runs use the metadata-rich artifact format.
- Raising the current benchmark sample count from 5 to 20 increases benchmark execution time. GitHub-hosted jobs allow up to six hours; the benchmark jobs use a 60-minute workflow timeout. Legacy baseline artifacts may still have five samples, so the report must disclose that difference.
- Existing successful `main` workflow runs have no package-coverage baseline artifact; package deltas remain unavailable until a successful run of the updated main workflow is retained.
- A retained `main` artifact may have been measured on a different runner instance or at a different time; `benchstat` cannot remove that environmental noise.
- `benchstat` results can be inconclusive; the comment must preserve that uncertainty.
- A baseline artifact can expire or be removed with its workflow run; the report must handle that without turning a missing baseline into a CI failure.
- Coverage detail and log excerpts must remain bounded to keep the report artifact and AI prompt compact.
- The existing same-repository PR guard and least-privilege workflow permissions remain in place.

## Validation

- Test coverage report generation using a small synthetic coverage profile with covered, partial, and zero-covered packages/functions.
- Verify benchmark reports preserve both revision identifiers and the `benchstat` output, including missing, expired, and inconclusive baseline cases.
- Verify failure diagnostics keep evidence grouped per job, include relevant context around failures, and stay within the size limit.
- Verify coverage comparisons identify package percentage-point changes, new/removed packages, missing baselines, and differing Go environments.
- Run workflow validation and the relevant script tests; inspect the generated JSON and prompt with representative success and failure fixtures.
