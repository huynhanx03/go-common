import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Writable } from "node:stream";
import { fileURLToPath } from "node:url";
import test from "node:test";
import semanticRelease from "semantic-release";

test("release advances manual tags, ignores unrelated tags, and does not repeat a released commit", async (t) => {
  const repo = repository(t);
  repo.git("tag", "v1.0.5");
  repo.git("push", "--tags", "origin");
  repo.git("switch", "-c", "unrelated");
  repo.commit("feat!: unrelated future API");
  repo.git("tag", "v9.0.0");
  repo.git("push", "--tags", "origin");
  repo.git("switch", "release");

  repo.commit("fix(toggle): keep last healthy catalog");
  const result = await repo.release();
  assert.equal(result?.nextRelease.version, "1.0.6");
  assert.match(result.nextRelease.notes, /keep last healthy catalog/);
  assert.equal(repo.git("rev-parse", "v1.0.6"), repo.git("rev-parse", "HEAD"));
  assert.match(repo.git("ls-remote", "--tags", "origin"), /refs\/tags\/v1\.0\.6\b/);
  assert.equal(await repo.release(), false);

  repo.commit("docs(i18n): explain money parameters");
  const documentation = await repo.release();
  assert.equal(documentation?.nextRelease.version, "1.0.7");
  assert.match(documentation.nextRelease.notes, /explain money parameters/);
  repo.commit("update housekeeping");
  assert.equal((await repo.release())?.nextRelease.version, "1.0.8");
  repo.commit("feature(i18n): add translation catalog");
  assert.equal((await repo.release())?.nextRelease.version, "1.1.0");
  repo.commit("feat(toggle): add scoped flags");
  assert.equal((await repo.release())?.nextRelease.version, "1.2.0");
});

test("first release bootstraps v1.0.0 and includes feature notes", async (t) => {
  const repo = repository(t);
  repo.commit("feat(i18n): add translation parameters");
  const result = await repo.release();
  assert.equal(result?.nextRelease.version, "1.0.0");
  assert.match(result.nextRelease.notes, /add translation parameters/);
  assert.match(result.nextRelease.notes, /go get github\.com\/huynhanx03\/go-common@v1\.0\.0/);
});

test("breaking release requires a matching Go module path before creating any tag", async (t) => {
  const repo = repository(t);
  repo.git("tag", "v1.2.0");
  repo.git("push", "--tags", "origin");
  repo.commit("feat(toggle)!: replace client initialization");
  await assert.rejects(repo.release(), /go\.mod|\/v2/);
  assert.doesNotMatch(repo.git("ls-remote", "--tags", "origin"), /refs\/tags\/v2\.0\.0\b/);

  repo.commit("refactor: migrate module path", "module github.com/huynhanx03/go-common/v2\n\ngo 1.23.0\n");
  const migrated = await repo.release();
  assert.equal(migrated?.nextRelease.version, "2.0.0");
  assert.match(migrated.nextRelease.notes, /go get github\.com\/huynhanx03\/go-common\/v2@v2\.0\.0/);
});

test("non-release branches never publish", async (t) => {
  const repo = repository(t);
  repo.git("switch", "-c", "develop");
  repo.commit("feat: work in progress");
  assert.equal(await repo.release(), false);
  assert.equal(repo.git("ls-remote", "--tags", "origin"), "");
});

test("workflow policy permits publication but rejects write tokens in PR CI and ungated publication", (t) => {
  const root = mkdtempSync(join(tmpdir(), "go-common-workflow-policy-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, "scripts"));
  cpSync(new URL("../../scripts/verify-workflows.sh", import.meta.url), join(root, "scripts/verify-workflows.sh"));
  cpSync(new URL("../workflows", import.meta.url), join(root, ".github/workflows"), { recursive: true });
  const verify = () => execFileSync("bash", [join(root, "scripts/verify-workflows.sh")], { stdio: "pipe" });
  assert.doesNotThrow(verify);

  const ciFile = join(root, ".github/workflows/ci.yml");
  const ci = readFileSync(ciFile, "utf8");
  writeFileSync(ciFile, ci.replace("contents: read", "contents: write"));
  assert.throws(verify);
  writeFileSync(ciFile, ci);

  const releaseFile = join(root, ".github/workflows/release.yml");
  const release = readFileSync(releaseFile, "utf8");
  writeFileSync(releaseFile, release.replace("needs: [verify]", "needs: []"));
  assert.throws(verify);
  writeFileSync(releaseFile, release.replace("      contents: write", "      contents: read"));
  assert.throws(verify);
});

function repository(t) {
  const root = mkdtempSync(join(tmpdir(), "go-common-release-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const remote = join(root, "remote.git");
  const cwd = join(root, "checkout");
  const env = {
    // Do not inherit GitHub's PR/ref detection or credentials into local-remote tests.
    PATH: process.env.PATH,
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: "/dev/null",
    GIT_AUTHOR_NAME: "Release test",
    GIT_AUTHOR_EMAIL: "release-test@example.invalid",
    GIT_COMMITTER_NAME: "Release test",
    GIT_COMMITTER_EMAIL: "release-test@example.invalid",
    GIT_TERMINAL_PROMPT: "0",
  };
  execFileSync("git", ["init", "--bare", "--initial-branch=release", remote], { env, stdio: "ignore" });
  execFileSync("git", ["clone", remote, cwd], { env, stdio: "ignore" });
  const git = (...args) => execFileSync("git", args, { cwd, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  let sequence = 0;
  const commit = (message, module) => {
    if (module) writeFileSync(join(cwd, "go.mod"), module);
    writeFileSync(join(cwd, "change.txt"), String(++sequence));
    git("add", ".");
    git("commit", "-m", message);
    git("push", "-u", "origin", "HEAD");
  };
  commit("chore: initialize module", "module github.com/huynhanx03/go-common\n\ngo 1.23.0\n");
  const release = () => {
    const output = new Writable({ write(_chunk, _encoding, done) { done(); } });
    return semanticRelease({
      extends: fileURLToPath(new URL("./release.config.mjs", import.meta.url)),
      // Load the production config exactly as the CLI does; disable GitHub API hooks only.
      verifyConditions: [],
      publish: [],
      success: [],
      fail: [],
      addChannel: [],
      repositoryUrl: `file://${remote}`,
      ci: false,
    }, { cwd, env, stdout: output, stderr: output });
  };
  return { git, commit, release };
}
