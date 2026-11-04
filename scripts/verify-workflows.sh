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
grep -Fq 'uses: gitleaks/gitleaks-action@' "$ci" ||
  fail 'CI must scan the repository for leaked secrets'
grep -Fq 'govulncheck@' "$ci" || fail 'CI must scan Go dependencies for known vulnerabilities'
grep -Fq 'workflow_call:' "$ci" || fail 'CI must be reusable by the release workflow'
grep -Fq 'make benchmarks > "$log_file"' "$ci" ||
  fail 'CI must run benchmarks'
grep -Fq 'BENCHMARK_RESULT: ${{ needs.benchmark.result }}' "$ci" ||
  fail 'CI report and required gate must observe the benchmark result'
grep -Fq '"$BENCHMARK_RESULT"' "$ci" ||
  fail 'CI required gate must fail when benchmarks do not pass'
grep -Fq 'needs: [unit, race, fuzz-seeds, integration, benchmark, test-overview, quality, security, gate]' "$ci" ||
  fail 'AI CI report must wait for every required CI job'
grep -Fq 'name: go-common-ai-ci-report' "$ci" ||
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
! grep -Fq 'actions/checkout@' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must not check out pull request code'
grep -Fq 'gh run download' "$workflow_root/ai-review.yml" ||
  fail 'AI CI review must read the report artifact from the completed CI run'
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

benchmark="$workflow_root/main.yml"
grep -Fq 'branches: [main]' "$benchmark" || fail 'benchmark workflow must run on main pushes only'
grep -Fq 'run: make benchmarks' "$benchmark" ||
  fail 'main benchmark workflow must run benchmarks'

printf '%s\n' 'workflow permissions, action pins, checkout credentials, and required gate verified'
