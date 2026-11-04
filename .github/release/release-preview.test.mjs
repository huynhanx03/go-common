import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createReleasePreview, createReleasePreviewFromGit } from "./release-preview.mjs";

test("creates release notes and next version from release commits", async () => {
  const preview = await createReleasePreview({
    cwd: process.cwd(),
    repositoryUrl: "https://github.com/huynhanx03/go-common",
    headSha: "head123",
    lastRelease: { version: "0.4.0", gitTag: "v0.4.0", gitHead: "base123" },
    commits: [
      { hash: "abc123", message: "feature: add a useful capability" },
      { hash: "def456", message: "fix: handle an edge case" },
    ],
  });

  assert.equal(preview.version, "0.5.0");
  assert.match(preview.notes, /### Features/);
  assert.match(preview.notes, /add a useful capability/);
  assert.match(preview.notes, /### Bug Fixes/);
  assert.match(preview.notes, /handle an edge case/);
  assert.match(preview.notes, /go get github\.com\/huynhanx03\/go-common@v0\.5\.0/);
});

test("returns no preview when there are no commits to release", async () => {
  const preview = await createReleasePreview({
    cwd: process.cwd(),
    repositoryUrl: "https://github.com/huynhanx03/go-common",
    headSha: "head123",
    lastRelease: { version: "0.4.0", gitTag: "v0.4.0", gitHead: "base123" },
    commits: [],
  });

  assert.equal(preview, null);
});

test("builds the preview from commits after the latest stable tag", async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), "go-common-release-preview-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));

  git(cwd, "init", "--quiet", "-b", "release");
  git(cwd, "config", "user.name", "Release Preview Test");
  git(cwd, "config", "user.email", "release-preview@example.com");
  git(cwd, "config", "commit.gpgsign", "false");
  await writeFile(join(cwd, "go.mod"), "module github.com/huynhanx03/go-common\n\ngo 1.24\n");
  await writeFile(join(cwd, "baseline.txt"), "baseline\n");
  git(cwd, "add", ".");
  git(cwd, "commit", "--quiet", "-m", "chore: baseline");
  git(cwd, "tag", "v0.4.0");

  await writeFile(join(cwd, "feature.txt"), "feature\n");
  git(cwd, "add", ".");
  git(cwd, "commit", "--quiet", "-m", "feature: add a release capability");

  await writeFile(join(cwd, "fix.txt"), "fix\n");
  git(cwd, "add", ".");
  git(cwd, "commit", "--quiet", "-m", "fix: correct release preview output");

  const preview = await createReleasePreviewFromGit({
    cwd,
    repositoryUrl: "https://github.com/huynhanx03/go-common",
  });

  assert.equal(preview.version, "0.5.0");
  assert.match(preview.notes, /add a release capability/);
  assert.match(preview.notes, /correct release preview output/);
  assert.doesNotMatch(preview.notes, /baseline/);
});

function git(cwd, ...args) {
  return execFileSync("git", args, { cwd, stdio: "ignore" });
}
