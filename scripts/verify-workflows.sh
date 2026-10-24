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
  ! grep -Eq '(^|[[:space:]])(contents|actions|checks|deployments|id-token|packages|pull-requests|security-events|statuses):[[:space:]]+write' "$workflow" ||
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
grep -Fq 'needs: [unit, race, fuzz-seeds, integration]' "$ci" ||
  fail 'CI test overview must wait for all test suites'
grep -Fq 'needs: [unit, race, fuzz-seeds, integration, test-overview, quality, security]' "$ci" ||
  fail 'CI required gate dependency graph is incomplete'
grep -Fq 'if: always()' "$ci" || fail 'CI overview and required gate must observe failed dependencies'
grep -Fq 'run: make test-integration' "$ci" ||
  fail 'container integration tests must run through the Makefile target'
grep -Fq 'uses: gitleaks/gitleaks-action@' "$ci" ||
  fail 'CI must scan the repository for leaked secrets'
grep -Fq 'govulncheck@' "$ci" || fail 'CI must scan Go dependencies for known vulnerabilities'

benchmark="$workflow_root/main.yml"
grep -Fq 'branches: [main]' "$benchmark" || fail 'benchmark workflow must run on main pushes only'
grep -Fq 'run: make benchmarks | tee benchmark.txt' "$benchmark" ||
  fail 'main benchmark workflow must capture the benchmark matrix'

printf '%s\n' 'workflow permissions, action pins, checkout credentials, and required gate verified'
