#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow_root="$root/.github/workflows"

fail() {
  printf 'workflow policy failed: %s\n' "$*" >&2
  exit 1
}

workflow_count=0
while IFS= read -r workflow; do
  workflow_count=$((workflow_count + 1))
  grep -Fq 'permissions:' "$workflow" || fail "$(basename "$workflow") has no explicit permissions"
  grep -Fq 'contents: read' "$workflow" || fail "$(basename "$workflow") lacks read-only contents permission"
  ! grep -Fq 'pull_request_target:' "$workflow" || fail "$(basename "$workflow") uses pull_request_target"
  ! grep -Eq 'permissions:[[:space:]]+write-all' "$workflow" || fail "$(basename "$workflow") grants write-all permissions"
  # Only the publish job in release.yml may write contents; PR/reusable CI stays read-only.
  disallowed_permissions="$(awk -v release="$(basename "$workflow")" '
    /^  [[:alnum:]_-]+:$/ { publish = ($0 == "  publish:") }
    release == "release.yml" && publish && /^      contents: write$/ { next }
    release == "release-preview.yml" && /^  preview:$/ { preview = 1; next }
    preview && /^  [[:alnum:]_-]+:$/ { preview = 0 }
    release == "release-preview.yml" && preview && /^      pull-requests: write$/ { next }
    release == "ai-review.yml" && /^  ai-review:$/ { ai_review = 1; next }
    ai_review && /^  [[:alnum:]_-]+:$/ { ai_review = 0 }
    release == "ai-review.yml" && ai_review && /^      (copilot-requests|pull-requests): write$/ { next }
    { print }
  ' "$workflow")"
  ! grep -Eq '(^|[[:space:]])(actions|attestations|checks|contents|copilot-requests|deployments|id-token|issues|packages|pull-requests|security-events|statuses):[[:space:]]+write' <<< "$disallowed_permissions" ||
    fail "$(basename "$workflow") grants a write permission"

  while IFS= read -r action; do
    [[ "$action" =~ @([0-9a-f]{40})([[:space:]]|$) ]] ||
      fail "$(basename "$workflow") contains an unpinned action: $action"
  done < <(grep -E '^[[:space:]]*-[[:space:]]+uses:[[:space:]]+' "$workflow")

  checkout_count="$(grep -Ec '^[[:space:]]*-[[:space:]]+uses:[[:space:]]+actions/checkout@' "$workflow" || true)"
  credential_fences="$(grep -Ec '^[[:space:]]+persist-credentials:[[:space:]]+false' "$workflow" || true)"
  [[ "$checkout_count" -eq "$credential_fences" ]] ||
    fail "$(basename "$workflow") must disable persisted credentials on every checkout"
done < <(find "$workflow_root" -maxdepth 1 -type f \( -name '*.yml' -o -name '*.yaml' \) -print | LC_ALL=C sort)
[[ "$workflow_count" -gt 0 ]] || fail 'no GitHub Actions workflows found'

ci="$workflow_root/ci.yml"
grep -Fq 'needs: [unit, race, fuzz-seeds, integration, benchmark]' "$ci" ||
  fail 'CI test overview must wait for all test suites and benchmarks'
grep -Fq 'needs: [unit, race, fuzz-seeds, integration, benchmark, test-overview, quality, security]' "$ci" ||
  fail 'CI required gate dependency graph is incomplete'
grep -Fq 'if: always()' "$ci" || fail 'CI overview and required gate must observe failed dependencies'
grep -Fq 'run: make test-integration' "$ci" ||
  fail 'container integration tests must run through the Makefile target'
grep -Fq 'python3 scripts/coverage_report.py' "$root/scripts/verify-coverage.sh" ||
  fail 'coverage verification must generate the detailed package report'
grep -Fq 'name: coverage-report' "$ci" ||
  fail 'CI must publish detailed coverage data for the AI review'
grep -Fq 'actions/download-artifact@' "$ci" ||
  fail 'PR summary must retrieve the detailed coverage report artifact'
grep -Fq 'zero_covered_functions_total:' "$ci" ||
  fail 'PR summary must include package and function coverage details'
grep -Fq "python3 -m unittest discover -s scripts -p 'test_*report.py'" "$ci" ||
  fail 'CI must run the coverage and benchmark report tests'
grep -Fq 'zero_covered_functions_total' "$ci" ||
  fail 'AI coverage summary must report zero-covered function totals and truncation'
grep -Fq 'uses: gitleaks/gitleaks-action@' "$ci" ||
  fail 'CI must scan the repository for leaked secrets'
grep -Fq 'govulncheck@' "$ci" || fail 'CI must scan Go dependencies for known vulnerabilities'
grep -Fq 'workflow_call:' "$ci" || fail 'CI must be reusable by the release workflow'
grep -Fq 'make benchmarks > "$BENCHMARK_FILE"' "$ci" ||
  fail 'CI must run benchmarks'
grep -Fq 'actions: read' "$ci" ||
  fail 'PR benchmark job must have read-only Actions access for baseline artifacts'
grep -Fq 'gh run list --repo "$REPOSITORY" --workflow main.yml' "$ci" ||
  fail 'PR benchmark job must locate the latest successful main benchmark run'
grep -Fq -- '--limit 100 --json databaseId,headSha,createdAt' "$ci" ||
  fail 'PR benchmark job must search retained successful main runs for the newest artifact'
grep -Fq 'gh run download "$run_id" --repo "$REPOSITORY" --name benchmark-baseline' "$ci" ||
  fail 'PR benchmark job must download the current-format main baseline artifact'
grep -Fq 'legacy_artifact="go-common-benchmark-$head_sha"' "$ci" ||
  fail 'PR benchmark job must fall back to retained legacy main benchmark artifacts'
grep -Fq '$0 == "benchmarks:" { in_benchmarks = 1; next }' "$ci" ||
  fail 'legacy benchmark sample count must be read from the benchmarks target only'
grep -Fq 'artifact_format: "legacy"' "$ci" ||
  fail 'PR benchmark job must mark metadata reconstructed from legacy artifacts'
grep -Fq 'scripts/benchmark_report.py' "$ci" ||
  fail 'PR benchmark job must publish an explicit comparison report'
grep -Fq 'golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d' "$ci" ||
  fail 'PR benchmark comparisons must use the pinned benchstat version'
grep -Fq 'name: benchmark-comparison' "$ci" ||
  fail 'CI must publish the benchmark comparison report for the AI summary'
grep -Fq 'benchmark_count 10' "$ci" ||
  fail 'PR benchmark metadata must record ten samples per benchmark'
grep -Fq 'benchmark_count 10' "$workflow_root/main.yml" ||
  fail 'main benchmark metadata must record ten samples per benchmark'
grep -Fq 'retention-days: 90' "$workflow_root/main.yml" ||
  fail 'main workflow must retain benchmark baselines for a bounded period'
grep -Fq -- '-benchmem -benchtime=500ms -count=10 ./...' "$root/Makefile" ||
  fail 'benchmark target must run ten samples with allocation metrics across all packages'
grep -Fq 'BENCHMARK_RESULT: ${{ needs.benchmark.result }}' "$ci" ||
  fail 'CI report and required gate must observe the benchmark result'
grep -Fq '"$BENCHMARK_RESULT"' "$ci" ||
  fail 'CI required gate must fail when benchmarks do not pass'
grep -Fq 'needs: [unit, race, fuzz-seeds, integration, benchmark, test-overview, quality, security, gate]' "$ci" ||
  fail 'AI CI report must wait for every required CI job'
grep -Fq 'name: pr-check-summary' "$ci" ||
  fail 'CI must publish a compact AI review report'
grep -Fq 'workflow_run:' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must run after the main CI workflow'
grep -Fq 'workflows: [ci]' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must target the main CI workflow'
grep -Fq 'types: [completed]' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must wait for CI completion'
grep -Fq "github.event.workflow_run.head_repository.full_name == github.repository" "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must be limited to same-repository pull requests'
grep -Fq "github.event.workflow_run.event == 'pull_request'" "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must only process pull request runs'
grep -Fq 'copilot-requests: write' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must request Copilot inference permission'
grep -Fq 'pull-requests: write' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must be able to comment on pull requests'
grep -Fq '      contents: read' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must be able to check out the default-branch report helper'
grep -Fq 'ref: ${{ github.event.repository.default_branch }}' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review may check out only the repository default branch'
grep -Fq 'gh run download "$RUN_ID" --repo "$REPOSITORY" --name pr-check-summary' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must download the PR summary artifact'
grep -Fq 'gh run view "$RUN_ID" --repo "$REPOSITORY" --json' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must fetch run details from GitHub'
grep -Fq 'gh run view "$RUN_ID" --repo "$REPOSITORY" --job "$job_id" --log-failed' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must fetch failed logs separately for each job'
grep -Fq 'status: "available", jobs:' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must normalize failed-job metadata before fetching logs'
grep -Fq 'python3 scripts/ci_failure_report.py' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must compact logs around failure markers'
grep -Fq 'failure_diagnostics' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must include per-job diagnostics in the prompt'
grep -Fq 'zero_covered_functions_total' "$workflow_root/ai-review.yml" ||
  fail 'AI prompt must report coverage gaps and omitted function counts'
grep -Fq 'p < 0.05' "$workflow_root/ai-review.yml" ||
  fail 'AI prompt must interpret benchmark significance conservatively'
grep -Fq 'Write the comment in English' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review comments must be written in English'
grep -Fq 'If sample counts differ, call that out' "$workflow_root/ai-review.yml" ||
  fail 'AI prompt must disclose unequal benchmark sample counts'
grep -Fq 'If baseline.artifact_format is legacy, say so' "$workflow_root/ai-review.yml" ||
  fail 'AI prompt must disclose legacy benchmark baselines'
! grep -Eq -- '--log-failed[^|]*\|[[:space:]]*tail' "$workflow_root/ai-review.yml" ||
  fail 'AI PR review must not use a global tail-only log excerpt'
grep -Fq 'copilot -p ' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must use GitHub Copilot CLI'
grep -Fq -- '--no-ask-user' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must run Copilot CLI non-interactively'
grep -Fq '<!-- go-common-ai-ci-review -->' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must update its existing PR comment'
! grep -Fq -- '--allow-tool' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must not grant Copilot CLI tool access'
release="$workflow_root/release.yml"
grep -Fq 'branches: [release]' "$release" || fail 'release workflow must target release pushes'
! grep -Eq '^[[:space:]]*(pull_request|pull_request_target|workflow_run):' "$release" ||
  fail 'release publication must not be triggered by PRs or another workflow run'
grep -Fq 'uses: ./.github/workflows/ci.yml' "$release" || fail 'release must reuse CI'
grep -Fq 'needs: [verify]' "$release" || fail 'publication must depend on successful CI'
grep -Fq "if: github.event_name == 'push' && github.ref == 'refs/heads/release'" "$release" ||
  fail 'publication must be limited to release branch pushes'
grep -Fq 'cancel-in-progress: false' "$release" || fail 'active releases must not be cancelled by newer pushes'
grep -Fq 'fetch-depth: 0' "$release" || fail 'release must fetch all history and tags'
grep -Fq 'ref: ${{ github.sha }}' "$release" || fail 'release must publish the verified commit'
[[ "$(grep -Ec '^      contents: write$' "$release" || true)" -eq 1 ]] ||
  fail 'release must grant contents-write exactly once, in the publish job'

release_preview="$workflow_root/release-preview.yml"
grep -Fq 'pull_request:' "$release_preview" || fail 'release preview must run on pull requests'
grep -Fq 'branches: [release]' "$release_preview" || fail 'release preview must target PRs into release'
grep -Fq 'pull-requests: write' "$release_preview" || fail 'release preview must be able to comment on PRs'
grep -Fq 'node .github/release/release-preview.mjs' "$release_preview" ||
  fail 'release preview must use the shared release-notes generator'
grep -Fq '<!-- go-common-release-preview -->' "$release_preview" ||
  fail 'release preview must update its existing PR comment'
! grep -Fq 'contents: write' "$release_preview" ||
  fail 'release preview must not be able to publish releases'

benchmark="$workflow_root/main.yml"
grep -Fq 'branches: [main]' "$benchmark" || fail 'benchmark workflow must run on main pushes only'
grep -Fq 'make benchmarks > "$log_file"' "$benchmark" ||
  fail 'main benchmark workflow must run benchmarks'

printf '%s\n' 'workflow permissions, action pins, checkout credentials, and required gate verified'
